package backup

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"time"

	"filippo.io/age"

	"github.com/codecolors/buddyget/internal/platform/validation"
)

// Une archive est un tar compressé en gzip, chiffré avec age quand une
// phrase de passe est configurée (format standard : `age -d` la déchiffre
// sans BuddyGet). Elle contient :
//   - manifest.json : application, format, date ;
//   - buddyget.db : instantané cohérent de la base ;
//   - attachments/… : les fichiers des pièces jointes.

const (
	manifestName = "manifest.json"
	dbName       = "buddyget.db"
	filesDir     = "attachments"
	format       = 1
	// maxRestoreBytes borne le contenu décompressé d'une archive restaurée
	// (protection contre une archive piégée).
	maxRestoreBytes = 8 << 30
	// scryptWorkFactor règle la dérivation de la clé depuis la phrase de
	// passe : 2^15 itérations demandent 32 Mo, compatibles avec la limite de
	// 128 Mo du conteneur (le réglage par défaut d'age, 2^18, en demande
	// 256). La longueur minimale de la phrase de passe compense.
	scryptWorkFactor = 15
)

var ageHeader = []byte("age-encryption.org/")

type manifest struct {
	Application string    `json:"application"`
	Format      int       `json:"format"`
	Created     time.Time `json:"created"`
}

// writeArchive écrit dans w l'archive de la base snapshot et des pièces
// jointes de filesPath.
func writeArchive(w io.Writer, passphrase, snapshot, filesPath string, now time.Time) error {
	out := w
	var enc io.WriteCloser
	if passphrase != "" {
		recipient, err := age.NewScryptRecipient(passphrase)
		if err != nil {
			return err
		}
		recipient.SetWorkFactor(scryptWorkFactor)
		if enc, err = age.Encrypt(w, recipient); err != nil {
			return err
		}
		out = enc
	}
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)
	m, err := json.Marshal(manifest{Application: "BuddyGet", Format: format, Created: now.UTC()})
	if err != nil {
		return err
	}
	if err := tw.WriteHeader(&tar.Header{Name: manifestName, Mode: 0o600, Size: int64(len(m)), ModTime: now}); err != nil {
		return err
	}
	if _, err := tw.Write(m); err != nil {
		return err
	}
	if err := addFile(tw, snapshot, dbName); err != nil {
		return err
	}
	entries, err := os.ReadDir(filesPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, e := range entries {
		if e.Type().IsRegular() {
			if err := addFile(tw, filepath.Join(filesPath, e.Name()), path.Join(filesDir, e.Name())); err != nil {
				return err
			}
		}
	}
	err = errors.Join(tw.Close(), gz.Close())
	if enc != nil {
		err = errors.Join(err, enc.Close())
	}
	return err
}

func addFile(tw *tar.Writer, src, name string) error {
	f, err := os.Open(src) //nolint:gosec // G304 : base ou pièce jointe de DATA_DIR.
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: info.Size(), ModTime: info.ModTime()}); err != nil {
		return err
	}
	_, err = io.Copy(tw, f)
	return err
}

// readArchive déchiffre et extrait une archive dans dir : la base, et les
// pièces jointes dans dir/attachments. Seuls les noms attendus sont
// acceptés.
func readArchive(r io.Reader, passphrase, dir string) error {
	br := bufio.NewReader(r)
	head, _ := br.Peek(len(ageHeader))
	var in io.Reader = br
	if bytes.Equal(head, ageHeader) {
		if passphrase == "" {
			return validation.Error("Cette sauvegarde est chiffrée : définissez BACKUP_PASSPHRASE pour la restaurer.")
		}
		identity, err := age.NewScryptIdentity(passphrase)
		if err != nil {
			return err
		}
		if in, err = age.Decrypt(br, identity); err != nil {
			return validation.Error("Impossible de déchiffrer la sauvegarde : phrase de passe différente ou fichier altéré.")
		}
	}
	gz, err := gzip.NewReader(in)
	if err != nil {
		return validation.Error("Ce fichier n'est pas une sauvegarde BuddyGet.")
	}
	defer func() { _ = gz.Close() }()
	if err := os.MkdirAll(filepath.Join(dir, filesDir), 0o750); err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	var seenManifest, seenDB bool
	budget := int64(maxRestoreBytes)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return validation.Error("Sauvegarde illisible ou tronquée.")
		}
		if h.Typeflag != tar.TypeReg || h.Size > budget {
			return validation.Error("Sauvegarde invalide : contenu inattendu.")
		}
		budget -= h.Size
		switch dirName, base := path.Split(h.Name); {
		case h.Name == manifestName:
			var m manifest
			if err := json.NewDecoder(io.LimitReader(tr, 1<<16)).Decode(&m); err != nil || m.Application != "BuddyGet" {
				return validation.Error("Ce fichier n'est pas une sauvegarde BuddyGet.")
			}
			if m.Format > format {
				return validation.Error("Sauvegarde produite par une version plus récente de BuddyGet.")
			}
			seenManifest = true
		case h.Name == dbName:
			if err := extract(tr, filepath.Join(dir, dbName)); err != nil {
				return err
			}
			seenDB = true
		case dirName == filesDir+"/" && base != "" && base != "." && base != "..":
			if err := extract(tr, filepath.Join(dir, filesDir, base)); err != nil {
				return err
			}
		default:
			return validation.Error("Sauvegarde invalide : contenu inattendu.")
		}
	}
	if !seenManifest || !seenDB {
		return validation.Error("Sauvegarde incomplète.")
	}
	return nil
}

func extract(r io.Reader, dest string) error {
	f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // G304 : nom d'entrée contrôlé par readArchive.
	if err != nil {
		return fmt.Errorf("extraction : %w", err)
	}
	_, err = io.Copy(f, r)
	return errors.Join(err, f.Sync(), f.Close())
}
