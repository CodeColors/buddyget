package db

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestOpenAppliesPragmasAndMigrations(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "x?#%.db") // caractères à échapper dans l'URI
	d, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := d.Close(); err != nil {
			t.Error(err)
		}
	}()

	for _, c := range []struct {
		db     *sql.DB
		pragma string
		want   string
	}{
		{d.W, "journal_mode", "wal"},
		{d.W, "foreign_keys", "1"},
		{d.W, "synchronous", "1"}, // NORMAL
		{d.R, "query_only", "1"},
		{d.R, "busy_timeout", "5000"},
	} {
		var got string
		if err := c.db.QueryRowContext(ctx, "PRAGMA "+c.pragma).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != c.want {
			t.Errorf("PRAGMA %s = %s, attendu %s", c.pragma, got, c.want)
		}
	}

	if _, err := d.R.ExecContext(ctx, "DELETE FROM users"); err == nil {
		t.Error("le pool de lecture ne doit pas pouvoir écrire")
	}
	if got := schemaVersion(t, d.W); got != countMigrations(t) {
		t.Errorf("version du schéma = %d, attendu %d", got, countMigrations(t))
	}
}

func TestMigrateIsIdempotentAndIncremental(t *testing.T) {
	ctx := t.Context()
	conn := openTest(t)
	fsys := fstest.MapFS{
		"migrations/0001_a.sql": {Data: []byte("CREATE TABLE a (x INTEGER) STRICT; CREATE TABLE b (y INTEGER) STRICT;")},
	}
	for range 2 {
		if err := migrate(ctx, conn, fsys); err != nil {
			t.Fatal(err)
		}
	}
	fsys["migrations/0002_c.sql"] = &fstest.MapFile{Data: []byte("CREATE TABLE c (z INTEGER) STRICT;")}
	if err := migrate(ctx, conn, fsys); err != nil {
		t.Fatal(err)
	}
	if got := schemaVersion(t, conn); got != 2 {
		t.Errorf("version = %d, attendu 2", got)
	}
	for _, table := range []string{"a", "b", "c"} {
		if _, err := conn.ExecContext(ctx, "INSERT INTO "+table+" VALUES (1)"); err != nil {
			t.Errorf("table %s absente : %v", table, err)
		}
	}
}

func TestMigrateRollsBackFailedMigration(t *testing.T) {
	ctx := t.Context()
	conn := openTest(t)
	fsys := fstest.MapFS{
		"migrations/0001_ok.sql":     {Data: []byte("CREATE TABLE a (x INTEGER) STRICT;")},
		"migrations/0002_cassee.sql": {Data: []byte("CREATE TABLE b (y INTEGER) STRICT; INSERT INTO inexistante VALUES (1);")},
	}
	if err := migrate(ctx, conn, fsys); err == nil || !strings.Contains(err.Error(), "0002_cassee.sql") {
		t.Fatalf("erreur attendue sur 0002_cassee.sql, obtenu %v", err)
	}
	if got := schemaVersion(t, conn); got != 1 {
		t.Errorf("version = %d, attendu 1", got)
	}
	if _, err := conn.ExecContext(ctx, "INSERT INTO b VALUES (1)"); err == nil {
		t.Error("la table b de la migration échouée ne doit pas exister")
	}
}

func TestMigrateRejectsInvalidSets(t *testing.T) {
	cases := map[string]fstest.MapFS{
		"nom sans numéro": {"migrations/init.sql": {}},
		"trou de numérotation": {
			"migrations/0001_a.sql": {Data: []byte("SELECT 1;")},
			"migrations/0003_c.sql": {Data: []byte("SELECT 1;")},
		},
		"numéro sur 3 chiffres": {"migrations/001_a.sql": {}},
	}
	for name, fsys := range cases {
		t.Run(name, func(t *testing.T) {
			if err := migrate(t.Context(), openTest(t), fsys); err == nil {
				t.Error("erreur attendue")
			}
		})
	}
}

func TestMigrateRejectsNewerDatabase(t *testing.T) {
	ctx := t.Context()
	conn := openTest(t)
	two := fstest.MapFS{
		"migrations/0001_a.sql": {Data: []byte("SELECT 1;")},
		"migrations/0002_b.sql": {Data: []byte("SELECT 1;")},
	}
	if err := migrate(ctx, conn, two); err != nil {
		t.Fatal(err)
	}
	delete(two, "migrations/0002_b.sql")
	if err := migrate(ctx, conn, two); err == nil {
		t.Error("une base plus récente que le programme doit être refusée")
	}
}

func openTest(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := open(filepath.Join(t.TempDir(), "test.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func schemaVersion(t *testing.T, conn *sql.DB) int {
	t.Helper()
	var v int
	if err := conn.QueryRowContext(t.Context(), "SELECT max(version) FROM schema_migrations").Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func countMigrations(t *testing.T) int {
	t.Helper()
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

func TestIsUniqueViolation(t *testing.T) {
	conn := openTest(t)
	ctx := t.Context()
	if _, err := conn.ExecContext(ctx, "CREATE TABLE u (x TEXT UNIQUE) STRICT; INSERT INTO u VALUES ('a')"); err != nil {
		t.Fatal(err)
	}
	_, err := conn.ExecContext(ctx, "INSERT INTO u VALUES ('a')")
	if !IsUniqueViolation(err) {
		t.Errorf("violation d'unicité non reconnue : %v", err)
	}
	_, err = conn.ExecContext(ctx, "INSERT INTO inexistante VALUES (1)")
	if err == nil || IsUniqueViolation(err) || IsForeignKeyViolation(err) {
		t.Errorf("autre erreur prise pour une violation de contrainte : %v", err)
	}
}

func TestIsForeignKeyViolation(t *testing.T) {
	d, err := Open(t.Context(), filepath.Join(t.TempDir(), "fk.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	_, err = d.W.ExecContext(t.Context(), "INSERT INTO sessions (token_hash, user_id, expires_at) VALUES (x'00', 999, 0)")
	if !IsForeignKeyViolation(err) || IsUniqueViolation(err) {
		t.Errorf("violation de clé étrangère non reconnue : %v", err)
	}
}

func TestCollect(t *testing.T) {
	conn := openTest(t)
	square := func(row Scanner) (int, error) {
		var n int
		err := row.Scan(&n)
		return n * n, err
	}
	got, err := Collect(t.Context(), conn, square, `SELECT value FROM json_each(?)`, "[1, 2, 3]")
	if err != nil || len(got) != 3 || got[2] != 9 {
		t.Errorf("lignes : %v, %v", got, err)
	}
	if _, err := Collect(t.Context(), conn, square, `SELECT 'x'`); err == nil {
		t.Error("erreur de lecture ignorée")
	}
	if _, err := Collect(t.Context(), conn, square, `SELECT FROM`); err == nil {
		t.Error("requête invalide acceptée")
	}
}
