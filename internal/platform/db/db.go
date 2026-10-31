// Package db ouvre la base SQLite (sans CGO), applique les réglages adaptés
// à un NAS peu puissant et exécute les migrations embarquées.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"runtime/debug"
	"time"

	_ "modernc.org/sqlite" // pilote "sqlite"
)

// readPoolSize borne le nombre de connexions de lecture simultanées.
const readPoolSize = 4

// pragmas communs à toutes les connexions. cache_size est en Kio quand il
// est négatif : 2 Mio par connexion au maximum.
var pragmas = []string{
	"busy_timeout(5000)",
	"foreign_keys(1)",
	"synchronous(NORMAL)",
	"cache_size(-2000)",
}

// DB sépare l'unique connexion d'écriture (SQLite sérialise de toute façon
// les écritures) d'un petit pool de lecture, qui profite du mode WAL.
type DB struct {
	W *sql.DB
	R *sql.DB
}

// Open ouvre (ou crée) la base au chemin donné, active le mode WAL et
// applique les migrations en attente.
func Open(ctx context.Context, path string) (*DB, error) {
	w, err := open(path, false)
	if err != nil {
		return nil, err
	}
	if _, err := w.ExecContext(ctx, "PRAGMA journal_mode = WAL"); err != nil {
		return nil, errors.Join(fmt.Errorf("activation du mode WAL : %w", err), w.Close())
	}
	if err := migrate(ctx, w, migrations); err != nil {
		return nil, errors.Join(err, w.Close())
	}
	r, err := open(path, true)
	if err != nil {
		return nil, errors.Join(err, w.Close())
	}
	return &DB{W: w, R: r}, nil
}

// Tx exécute fn dans une transaction d'écriture, validée si fn réussit et
// annulée sinon. Les lectures de contrôle faites dans fn voient l'état
// exact sur lequel portent les écritures.
func (d *DB) Tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := d.W.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return errors.Join(err, tx.Rollback())
	}
	return tx.Commit()
}

// ReleaseMemory rend au système la mémoire de travail laissée par une
// opération lourde et ponctuelle (import de milliers de lignes) : SQLite
// libère le cache de la connexion d'écriture, Go son tas inutilisé. Sans
// cela, rien ne la reprendrait au repos, faute d'activité.
func (d *DB) ReleaseMemory(ctx context.Context) {
	if _, err := d.W.ExecContext(ctx, "PRAGMA shrink_memory"); err != nil {
		slog.WarnContext(ctx, "libération de la mémoire SQLite", "err", err)
	}
	debug.FreeOSMemory()
}

// Close ferme les deux pools ; la dernière fermeture rapatrie le WAL dans
// le fichier principal.
func (d *DB) Close() error {
	return errors.Join(d.R.Close(), d.W.Close())
}

func open(path string, readOnly bool) (*sql.DB, error) {
	q := url.Values{"_pragma": pragmas}
	if readOnly {
		q.Add("_pragma", "query_only(1)")
	} else {
		// Les transactions d'écriture prennent le verrou dès BEGIN :
		// pas d'échec tardif lors de la promotion lecture → écriture.
		q.Set("_txlock", "immediate")
	}
	dsn := url.URL{Scheme: "file", Path: path, RawQuery: q.Encode()}
	conn, err := sql.Open("sqlite", dsn.String())
	if err != nil {
		return nil, fmt.Errorf("ouverture de %s : %w", path, err)
	}
	if readOnly {
		conn.SetMaxOpenConns(readPoolSize)
		conn.SetMaxIdleConns(readPoolSize)
		// Au repos, les connexions de lecture (et leur cache) sont libérées.
		conn.SetConnMaxIdleTime(5 * time.Minute)
	} else {
		conn.SetMaxOpenConns(1)
	}
	return conn, nil
}
