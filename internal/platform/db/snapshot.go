package db

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
)

// Snapshot écrit une copie cohérente de la base dans dest (qui ne doit pas
// exister) : VACUUM INTO lit un instantané, sans bloquer les lectures.
func (d *DB) Snapshot(ctx context.Context, dest string) error {
	_, err := d.W.ExecContext(ctx, `VACUUM INTO ?`, dest)
	return err
}

// Verify contrôle une base avant de la restaurer : intégrité SQLite et
// version du schéma, au plus celle de ce programme (les migrations
// manquantes s'appliqueront à l'ouverture).
func Verify(ctx context.Context, path string) error {
	conn, err := open(path, true)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	var check string
	if err := conn.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&check); err != nil {
		return fmt.Errorf("lecture de la base : %w", err)
	}
	if check != "ok" {
		return fmt.Errorf("base endommagée : %s", check)
	}
	var version int
	if err := conn.QueryRowContext(ctx, `SELECT coalesce(max(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return errors.New("ce fichier n'est pas une base BuddyGet")
	}
	files, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return err
	}
	if version > len(files) {
		return fmt.Errorf("la sauvegarde vient d'une version plus récente de BuddyGet (schéma %d, ce programme %d)", version, len(files))
	}
	return nil
}
