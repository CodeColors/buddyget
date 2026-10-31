// Package attachments gère les pièces jointes des opérations (photo ou PDF
// d'un ticket, d'une facture). Les images sont réduites à 1 600 px de large
// et recompressées en JPEG côté serveur ; les PDF sont gardés tels quels.
// Les fichiers vivent dans DATA_DIR/attachments, sous un nom aléatoire.
package attachments

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/codecolors/buddyget/internal/platform/db"
	"github.com/codecolors/buddyget/internal/platform/validation"
)

// ErrNotFound signale une pièce jointe inexistante.
var ErrNotFound = errors.New("pièce jointe introuvable")

const nameMaxLen = 100

// Kind est la nature d'une pièce jointe.
type Kind string

const (
	Image Kind = "image"
	PDF   Kind = "pdf"
)

// Attachment est une pièce jointe.
type Attachment struct {
	ID            int64
	TransactionID int64
	Name          string // nom d'origine, pour l'affichage
	Kind          Kind
	Size          int64 // octets du fichier conservé
	File          string
}

// ContentType renvoie le type MIME du fichier conservé.
func (a Attachment) ContentType() string {
	if a.Kind == PDF {
		return "application/pdf"
	}
	return "image/jpeg"
}

// Service gère les pièces jointes.
type Service struct {
	db      *db.DB
	dir     string
	maxSize int64 // taille maximale d'un fichier déposé
	quota   int64 // place maximale occupée par l'ensemble des pièces
	// images sérialise la réduction des images : une seule à la fois en
	// mémoire, quel que soit le nombre d'envois simultanés.
	images chan struct{}
}

// NewService crée le service des pièces jointes.
func NewService(d *db.DB, dataDir string, maxSize, quota int64) *Service {
	return &Service{db: d, dir: filepath.Join(dataDir, "attachments"), maxSize: maxSize, quota: quota, images: make(chan struct{}, 1)}
}

// path renvoie le chemin du fichier d'une pièce jointe.
func (s *Service) path(file string) string { return filepath.Join(s.dir, file) }

// Open ouvre le fichier d'une pièce jointe.
func (s *Service) Open(ctx context.Context, id int64) (Attachment, *os.File, error) {
	a, err := s.get(ctx, id)
	if err != nil {
		return a, nil, err
	}
	f, err := os.Open(s.path(a.File))
	return a, f, err
}

const attachmentSelect = `SELECT id, transaction_id, name, kind, size, file FROM attachments`

func scan(row interface{ Scan(...any) error }) (Attachment, error) {
	var a Attachment
	err := row.Scan(&a.ID, &a.TransactionID, &a.Name, &a.Kind, &a.Size, &a.File)
	return a, err
}

func (s *Service) get(ctx context.Context, id int64) (Attachment, error) {
	a, err := scan(s.db.R.QueryRowContext(ctx, attachmentSelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	return a, err
}

// List renvoie les pièces jointes d'une opération.
func (s *Service) List(ctx context.Context, transactionID int64) ([]Attachment, error) {
	rows, err := s.db.R.QueryContext(ctx, attachmentSelect+` WHERE transaction_id = ? ORDER BY id`, transactionID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Attachment
	for rows.Next() {
		a, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Add joint un fichier (photo ou PDF) à une opération.
func (s *Service) Add(ctx context.Context, transactionID int64, name string, r io.Reader) (Attachment, error) {
	a := Attachment{TransactionID: transactionID, Name: displayName(name)}
	data, err := io.ReadAll(io.LimitReader(r, s.maxSize+1))
	switch {
	case err != nil:
		return a, err
	case int64(len(data)) > s.maxSize:
		return a, validation.Errorf("Fichier trop volumineux : %d Mo au plus.", s.maxSize>>20)
	case len(data) == 0:
		return a, validation.Error("Le fichier est vide.")
	}
	var ok bool
	if err := s.db.R.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM transactions WHERE id = ?)`, transactionID).Scan(&ok); err != nil {
		return a, err
	}
	if !ok {
		return a, ErrNotFound
	}

	ext := ".pdf"
	switch http.DetectContentType(data) {
	case "application/pdf":
		a.Kind = PDF
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		a.Kind, ext = Image, ".jpg"
		s.images <- struct{}{}
		data, err = shrink(data)
		<-s.images
		if err != nil {
			return a, err
		}
	default:
		return a, validation.Error("Format non pris en charge : photo (JPEG, PNG, WebP, GIF) ou PDF.")
	}
	a.Size = int64(len(data))

	var used int64
	if err := s.db.R.QueryRowContext(ctx, `SELECT coalesce(sum(size), 0) FROM attachments`).Scan(&used); err != nil {
		return a, err
	}
	if used+a.Size > s.quota {
		return a, validation.Errorf("Espace des pièces jointes plein (%d Mo) : supprimez-en ou augmentez ATTACHMENTS_QUOTA_MB.", s.quota>>20)
	}
	if a.File, err = s.write(data, ext); err != nil {
		return a, err
	}
	res, err := s.db.W.ExecContext(ctx, `INSERT INTO attachments (transaction_id, name, kind, size, file) VALUES (?, ?, ?, ?, ?)`,
		a.TransactionID, a.Name, a.Kind, a.Size, a.File)
	if err == nil {
		a.ID, err = res.LastInsertId()
	}
	if err != nil {
		_ = os.Remove(s.path(a.File))
	}
	return a, err
}

// write enregistre un fichier sous un nom aléatoire, en écrivant d'abord
// un fichier temporaire : jamais de fichier tronqué.
func (s *Service) write(data []byte, ext string) (string, error) {
	if err := os.MkdirAll(s.dir, 0o750); err != nil {
		return "", err
	}
	token := make([]byte, 16)
	if _, err := rand.Read(token); err != nil {
		return "", err
	}
	file := hex.EncodeToString(token) + ext
	tmp := s.path(file + ".tmp")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, s.path(file)); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return file, nil
}

// Delete supprime une pièce jointe et son fichier.
func (s *Service) Delete(ctx context.Context, id int64) error {
	a, err := s.get(ctx, id)
	if err != nil {
		return err
	}
	if _, err := s.db.W.ExecContext(ctx, `DELETE FROM attachments WHERE id = ?`, id); err != nil {
		return err
	}
	if err := os.Remove(s.path(a.File)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Sweep supprime les fichiers qui ne sont plus rattachés à une pièce jointe
// (opération supprimée, import annulé) et renvoie leur nombre.
func (s *Service) Sweep(ctx context.Context) (int, error) {
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	known := map[string]bool{}
	rows, err := s.db.R.QueryContext(ctx, `SELECT file FROM attachments`)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var f string
		if err := rows.Scan(&f); err != nil {
			return 0, err
		}
		known[f] = true
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	removed := 0
	for _, e := range entries {
		if e.IsDir() || known[e.Name()] {
			continue
		}
		if err := os.Remove(s.path(e.Name())); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

// displayName garde le nom de base du fichier d'origine, raccourci.
func displayName(name string) string {
	name = strings.TrimSpace(filepath.Base(strings.ReplaceAll(name, `\`, "/")))
	if name == "" || name == "." || name == "/" {
		return "pièce jointe"
	}
	for utf8.RuneCountInString(name) > nameMaxLen {
		_, size := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-size]
	}
	return name
}
