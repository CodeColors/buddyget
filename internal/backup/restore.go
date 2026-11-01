package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/codecolors/buddyget/internal/platform/db"
	"github.com/codecolors/buddyget/internal/platform/validation"
)

// La restauration se fait en deux temps : l'archive est déchiffrée,
// extraite et vérifiée dans DATA_DIR/restauration pendant que BuddyGet
// tourne, puis appliquée au démarrage suivant, avant l'ouverture de la
// base. Les données remplacées sont mises de côté dans
// DATA_DIR/avant-restauration.

const (
	pendingDir  = "restauration"
	stagingDir  = ".restauration-partielle"
	previousDir = "avant-restauration"
)

// Stage prépare la restauration d'une archive lue dans r.
func (s *Service) Stage(ctx context.Context, r io.Reader) error {
	if !s.mu.TryLock() {
		return ErrBusy
	}
	defer s.mu.Unlock()
	staging := filepath.Join(s.dataDir, stagingDir)
	if err := os.RemoveAll(staging); err != nil {
		return err
	}
	if err := readArchive(r, s.passphrase, staging); err != nil {
		return errors.Join(err, os.RemoveAll(staging))
	}
	if err := db.Verify(ctx, filepath.Join(staging, dbName)); err != nil {
		return errors.Join(validation.Errorf("Base de la sauvegarde refusée : %v.", err), os.RemoveAll(staging))
	}
	pending := filepath.Join(s.dataDir, pendingDir)
	if err := os.RemoveAll(pending); err != nil {
		return err
	}
	return os.Rename(staging, pending)
}

// StageArchive prépare la restauration d'une archive conservée.
func (s *Service) StageArchive(ctx context.Context, name string) error {
	f, err := s.Open(name)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return s.Stage(ctx, f)
}

// Pending indique qu'une restauration attend le prochain démarrage.
func (s *Service) Pending() bool {
	_, err := os.Stat(filepath.Join(s.dataDir, pendingDir))
	return err == nil
}

// ApplyPending applique, au démarrage et avant l'ouverture de la base, une
// restauration préparée : la base et les pièces jointes en place sont
// déplacées dans DATA_DIR/avant-restauration (la précédente copie est
// remplacée), puis celles de la sauvegarde prennent leur place. Faux s'il
// n'y avait rien à restaurer.
func ApplyPending(dataDir string, now time.Time) (bool, error) {
	pending := filepath.Join(dataDir, pendingDir)
	if _, err := os.Stat(pending); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	previous := filepath.Join(dataDir, previousDir)
	if err := os.RemoveAll(previous); err != nil {
		return false, err
	}
	if err := os.MkdirAll(previous, 0o750); err != nil {
		return false, err
	}
	for _, name := range []string{dbName, dbName + "-wal", dbName + "-shm", filesDir} {
		if err := move(filepath.Join(dataDir, name), filepath.Join(previous, name)); err != nil {
			return false, err
		}
	}
	if err := os.WriteFile(filepath.Join(previous, "LISEZ-MOI.txt"),
		fmt.Appendf(nil, "Données remplacées par la restauration du %s.\n", now.Format("02/01/2006 à 15:04")), 0o600); err != nil {
		return false, err
	}
	for _, name := range []string{dbName, filesDir} {
		if err := move(filepath.Join(pending, name), filepath.Join(dataDir, name)); err != nil {
			return false, err
		}
	}
	return true, os.RemoveAll(pending)
}

// move déplace src vers dest ; sans effet si src n'existe pas.
func move(src, dest string) error {
	err := os.Rename(src, dest)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
