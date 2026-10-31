package db

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrations embed.FS

// migrate applique, dans l'ordre et chacune dans sa transaction, les
// migrations de fsys (migrations/NNNN_description.sql) absentes de la
// table schema_migrations. Les numéros doivent se suivre à partir de 1.
func migrate(ctx context.Context, db *sql.DB, fsys fs.FS) error {
	files, err := fs.Glob(fsys, "migrations/*.sql") // tri lexical garanti
	if err != nil {
		return err
	}
	for i, f := range files {
		if v, err := migrationVersion(f); err != nil || v != i+1 {
			return fmt.Errorf("migration %s : numéro %04d attendu (NNNN_description.sql)", f, i+1)
		}
	}

	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
	) STRICT`); err != nil {
		return fmt.Errorf("création de schema_migrations : %w", err)
	}
	var current int
	if err := db.QueryRowContext(ctx, `SELECT coalesce(max(version), 0) FROM schema_migrations`).Scan(&current); err != nil {
		return fmt.Errorf("lecture de la version du schéma : %w", err)
	}
	if current > len(files) {
		return fmt.Errorf("la base est en version %d, plus récente que ce programme (%d) : mettez BuddyGet à jour", current, len(files))
	}

	for i, f := range files[current:] {
		version := current + i + 1
		script, err := fs.ReadFile(fsys, f)
		if err != nil {
			return err
		}
		if err := applyMigration(ctx, db, version, string(script)); err != nil {
			return fmt.Errorf("migration %s : %w", f, err)
		}
		slog.InfoContext(ctx, "migration appliquée", "version", version)
	}
	return nil
}

func migrationVersion(file string) (int, error) {
	prefix, _, ok := strings.Cut(path.Base(file), "_")
	if !ok || len(prefix) != 4 {
		return 0, errors.New("nom de migration invalide")
	}
	return strconv.Atoi(prefix)
}

func applyMigration(ctx context.Context, db *sql.DB, version int, script string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, script); err != nil {
		return errors.Join(err, tx.Rollback())
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version) VALUES (?)`, version); err != nil {
		return errors.Join(err, tx.Rollback())
	}
	return tx.Commit()
}
