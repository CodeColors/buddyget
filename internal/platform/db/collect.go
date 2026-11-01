package db

import (
	"context"
	"database/sql"
)

// Scanner est une ligne de résultat (sql.Row ou sql.Rows).
type Scanner interface {
	Scan(dest ...any) error
}

// Collect exécute une requête et lit chacune de ses lignes avec scan ; le
// curseur est fermé avant le retour, ce qui libère la connexion pour les
// requêtes suivantes.
func Collect[T any](ctx context.Context, q *sql.DB, scan func(Scanner) (T, error), query string, args ...any) ([]T, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []T
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
