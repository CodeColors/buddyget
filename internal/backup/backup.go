// Package backup sauvegarde et restaure BuddyGet : une archive cohérente
// de la base et des pièces jointes, chiffrée au besoin, conservée dans
// DATA_DIR/backups avec rotation des sauvegardes automatiques, copiée si on
// le souhaite vers un second emplacement (dossier monté ou WebDAV), et
// restaurée au démarrage suivant.
package backup

import (
	"cmp"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/codecolors/buddyget/internal/platform/db"
	"github.com/codecolors/buddyget/internal/platform/validation"
)

// ErrNotFound signale une archive inexistante.
var ErrNotFound = errors.New("sauvegarde introuvable")

// ErrBusy signale une sauvegarde ou une restauration déjà en cours.
var ErrBusy = validation.Error("Une sauvegarde est déjà en cours, réessayez dans un instant.")

// archiveName reconnaît le nom d'une archive : manuelle ou automatique,
// chiffrée ou non.
var archiveName = regexp.MustCompile(`^buddyget-(auto-)?(\d{8}-\d{6})\.tar\.gz(\.age)?$`)

// Archive est une sauvegarde conservée.
type Archive struct {
	Name      string
	Size      int64
	Created   time.Time
	Auto      bool // sauvegarde automatique, soumise à la rotation
	Encrypted bool
}

// Copy est la destination de la copie externe.
type Copy string

const (
	NoCopy     Copy = "aucune"
	FolderCopy Copy = "dossier"
	WebDAVCopy Copy = "webdav"
)

// Settings sont les réglages des sauvegardes.
type Settings struct {
	Auto           bool // une sauvegarde chaque nuit
	Keep           int  // sauvegardes automatiques conservées
	Copy           Copy
	CopyDir        string // dossier monté dans le conteneur
	WebDAVURL      string
	WebDAVUser     string
	WebDAVPassword string
	LastAt         string // dernière sauvegarde réussie (RFC 3339), vide sinon
	LastError      string // échec de la dernière tentative
}

// Service gère les sauvegardes.
type Service struct {
	db         *db.DB
	dataDir    string
	dir        string
	passphrase string
	client     *http.Client
	mu         sync.Mutex // une sauvegarde ou une restauration à la fois
}

// NewService crée le service des sauvegardes ; passphrase vide : archives
// non chiffrées.
func NewService(d *db.DB, dataDir, passphrase string) *Service {
	return &Service{
		db: d, dataDir: dataDir, dir: filepath.Join(dataDir, "backups"), passphrase: passphrase,
		client: &http.Client{Timeout: 30 * time.Minute},
	}
}

// Encrypted indique si les sauvegardes sont chiffrées.
func (s *Service) Encrypted() bool { return s.passphrase != "" }

// Settings renvoie les réglages.
func (s *Service) Settings(ctx context.Context) (Settings, error) {
	var st Settings
	err := s.db.R.QueryRowContext(ctx, `
		SELECT auto, keep, copy_kind, copy_dir, webdav_url, webdav_user, webdav_password, last_at, last_error FROM backup_settings`).
		Scan(&st.Auto, &st.Keep, &st.Copy, &st.CopyDir, &st.WebDAVURL, &st.WebDAVUser, &st.WebDAVPassword, &st.LastAt, &st.LastError)
	return st, err
}

// SaveSettings enregistre les réglages ; un mot de passe WebDAV vide
// conserve le précédent.
func (s *Service) SaveSettings(ctx context.Context, st Settings) error {
	st.CopyDir, st.WebDAVURL, st.WebDAVUser = strings.TrimSpace(st.CopyDir), strings.TrimSpace(st.WebDAVURL), strings.TrimSpace(st.WebDAVUser)
	switch {
	case st.Keep < 1 || st.Keep > 90:
		return validation.Error("Conservez de 1 à 90 sauvegardes automatiques.")
	case !slices.Contains([]Copy{NoCopy, FolderCopy, WebDAVCopy}, st.Copy):
		return validation.Error("Choisissez la copie vers un second emplacement.")
	}
	switch st.Copy {
	case FolderCopy:
		if !filepath.IsAbs(st.CopyDir) {
			return validation.Error("Indiquez le chemin absolu du dossier de copie, tel que le voit le conteneur (par exemple /copie).")
		}
		if info, err := os.Stat(st.CopyDir); err != nil || !info.IsDir() {
			return validation.Error("Dossier de copie introuvable : montez-le dans le conteneur (docker-compose.yml).")
		}
		if filepath.Clean(st.CopyDir) == filepath.Clean(s.dir) {
			return validation.Error("Le dossier de copie doit être un autre emplacement que DATA_DIR/backups.")
		}
	case WebDAVCopy:
		u, err := url.Parse(st.WebDAVURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return validation.Error("Indiquez l'adresse WebDAV du dossier de copie (https://…).")
		}
	}
	_, err := s.db.W.ExecContext(ctx, `
		UPDATE backup_settings SET auto = ?, keep = ?, copy_kind = ?, copy_dir = ?, webdav_url = ?, webdav_user = ?,
			webdav_password = CASE WHEN ? = '' THEN webdav_password ELSE ? END`,
		st.Auto, st.Keep, st.Copy, st.CopyDir, st.WebDAVURL, st.WebDAVUser, st.WebDAVPassword, st.WebDAVPassword)
	return err
}

func (s *Service) record(ctx context.Context, at time.Time, failure error) {
	var err error
	if failure == nil {
		_, err = s.db.W.ExecContext(ctx, `UPDATE backup_settings SET last_at = ?, last_error = ''`, at.Format(time.RFC3339))
	} else {
		_, err = s.db.W.ExecContext(ctx, `UPDATE backup_settings SET last_error = ?`, at.Format("02/01/2006 15:04")+" : "+message(failure))
	}
	if err != nil {
		slog.ErrorContext(ctx, "compte rendu de sauvegarde", "err", err)
	}
}

// message renvoie le texte d'une erreur, sans détail technique pour une
// erreur de saisie.
func message(err error) string {
	if msg, ok := validation.Message(err); ok {
		return msg
	}
	return err.Error()
}

// List renvoie les archives, de la plus récente à la plus ancienne.
func (s *Service) List() ([]Archive, error) { return list(s.dir) }

func list(dir string) ([]Archive, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Archive
	for _, e := range entries {
		m := archiveName.FindStringSubmatch(e.Name())
		if m == nil || !e.Type().IsRegular() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return nil, err
		}
		created, _ := time.ParseInLocation("20060102-150405", m[2], time.Local)
		out = append(out, Archive{Name: e.Name(), Size: info.Size(), Created: created, Auto: m[1] != "", Encrypted: m[3] != ""})
	}
	slices.SortFunc(out, func(a, b Archive) int { return cmp.Or(b.Created.Compare(a.Created), strings.Compare(b.Name, a.Name)) })
	return out, nil
}

// Open ouvre une archive pour la télécharger.
func (s *Service) Open(name string) (*os.File, error) {
	if !archiveName.MatchString(name) {
		return nil, ErrNotFound
	}
	f, err := os.Open(filepath.Join(s.dir, name)) //nolint:gosec // G304 : nom vérifié par archiveName.
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	return f, err
}

// Delete supprime une archive.
func (s *Service) Delete(name string) error {
	if !archiveName.MatchString(name) {
		return ErrNotFound
	}
	err := os.Remove(filepath.Join(s.dir, name))
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	return err
}

// create écrit une sauvegarde dans DATA_DIR/backups ; auto la soumet à la
// rotation.
func (s *Service) create(ctx context.Context, auto bool, now time.Time) (Archive, error) {
	if err := os.MkdirAll(s.dir, 0o750); err != nil {
		return Archive{}, err
	}
	kind := ""
	if auto {
		kind = "auto-"
	}
	name := fmt.Sprintf("buddyget-%s%s.tar.gz", kind, now.Format("20060102-150405"))
	if s.Encrypted() {
		name += ".age"
	}
	snapshot := filepath.Join(s.dir, ".instantane-"+rand.Text()+".db")
	defer func() { _ = os.Remove(snapshot) }()
	if err := s.db.Snapshot(ctx, snapshot); err != nil {
		return Archive{}, fmt.Errorf("instantané de la base : %w", err)
	}
	if err := writeFile(filepath.Join(s.dir, name), func(f *os.File) error {
		return writeArchive(f, s.passphrase, snapshot, filepath.Join(s.dataDir, filesDir), now)
	}); err != nil {
		return Archive{}, err
	}
	info, err := os.Stat(filepath.Join(s.dir, name))
	if err != nil {
		return Archive{}, err
	}
	return Archive{Name: name, Size: info.Size(), Created: now, Auto: auto, Encrypted: s.Encrypted()}, nil
}

// writeFile écrit dest par un fichier temporaire du même dossier, renommé
// une fois complet et synchronisé : jamais d'archive tronquée sous son nom
// définitif.
func writeFile(dest string, fill func(*os.File) error) error {
	tmp := filepath.Join(filepath.Dir(dest), "."+filepath.Base(dest)+".partiel")
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) //nolint:gosec // G304 : nommé d'après une archive du service.
	if err != nil {
		return err
	}
	if err := errors.Join(fill(f), f.Sync(), f.Close()); err != nil {
		return errors.Join(err, os.Remove(tmp))
	}
	return os.Rename(tmp, dest)
}

// rotate supprime les sauvegardes automatiques de dir au-delà des keep plus
// récentes.
func rotate(dir string, keep int) error {
	archives, err := list(dir)
	if err != nil {
		return err
	}
	kept := 0
	for _, a := range archives {
		if !a.Auto {
			continue
		}
		if kept++; kept > keep {
			if err := os.Remove(filepath.Join(dir, a.Name)); err != nil {
				return err
			}
		}
	}
	return nil
}

// Daily est la tâche quotidienne : sauvegarde automatique si elle est
// activée, rotation puis copie. Un échec est consigné dans les réglages.
func (s *Service) Daily(ctx context.Context, now time.Time) error {
	st, err := s.Settings(ctx)
	if err != nil || !st.Auto {
		return err
	}
	_, err = s.Run(ctx, st, true, now)
	return err
}

// Run fait une sauvegarde complète : archive, rotation des sauvegardes
// automatiques, copie vers le second emplacement ; le résultat est
// consigné.
func (s *Service) Run(ctx context.Context, st Settings, auto bool, now time.Time) (Archive, error) {
	if !s.mu.TryLock() {
		return Archive{}, ErrBusy
	}
	defer s.mu.Unlock()
	// La dérivation de la clé (32 Mo) et la compression laissent un tas
	// inutile au repos : il est rendu au système.
	defer s.db.ReleaseMemory(ctx)
	a, err := s.create(ctx, auto, now)
	if err == nil {
		err = rotate(s.dir, st.Keep)
	}
	if err == nil {
		err = s.copy(ctx, st, a)
	}
	s.record(ctx, now, err)
	return a, err
}
