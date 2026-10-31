package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/codecolors/buddyget/internal/platform/validation"
)

// Selection désigne les opérations visées par une édition par lot : soit
// une liste d'identifiants, soit tous les résultats d'une recherche.
type Selection struct {
	IDs    []int64
	Filter *Filter
}

// BatchChange décrit les modifications d'un lot.
type BatchChange struct {
	SetCategory bool
	CategoryID  int64 // 0 : retirer la catégorie
	AccountID   int64 // 0 : compte inchangé
}

// BatchResult rend compte d'une édition par lot. Les jambes de virement ne
// sont jamais modifiées par lot (catégorie et compte y sont imposés).
type BatchResult struct {
	Updated          int
	SkippedTransfers int
}

// BatchUpdate applique les modifications à la sélection, en une transaction.
func (s *Service) BatchUpdate(ctx context.Context, sel Selection, ch BatchChange) (BatchResult, error) {
	if !ch.SetCategory && ch.AccountID == 0 {
		return BatchResult{}, validation.Error("Choisissez une modification à appliquer.")
	}
	selected, selArgs, err := sel.sql()
	if err != nil {
		return BatchResult{}, err
	}

	var res BatchResult
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		if ch.AccountID != 0 {
			if err := checkAccount(ctx, tx, ch.AccountID); err != nil {
				return err
			}
		}
		if ch.SetCategory && ch.CategoryID != 0 {
			ok, err := exists(ctx, tx, `SELECT EXISTS (SELECT 1 FROM categories WHERE id = ?)`, ch.CategoryID)
			if err != nil {
				return err
			}
			if !ok {
				return validation.Error("Catégorie introuvable.")
			}
		}
		if err := tx.QueryRowContext(ctx,
			`SELECT count(*) FROM transactions WHERE transfer_id IS NOT NULL AND id IN (`+selected+`)`, selArgs...,
		).Scan(&res.SkippedTransfers); err != nil {
			return err
		}

		var sets []string
		var args []any
		if ch.SetCategory {
			sets = append(sets, `category_id = nullif(?, 0)`)
			args = append(args, ch.CategoryID)
		}
		if ch.AccountID != 0 {
			sets = append(sets, `account_id = ?`)
			args = append(args, ch.AccountID)
		}
		update := `UPDATE transactions SET ` + strings.Join(sets, ", ") + //nolint:gosec // G202 : fragments constants, valeurs en paramètres.
			` WHERE transfer_id IS NULL AND id IN (` + selected + `)`
		r, err := tx.ExecContext(ctx, update, append(args, selArgs...)...)
		if err != nil {
			return err
		}
		n, err := r.RowsAffected()
		res.Updated = int(n)
		return err
	})
	return res, err
}

// sql renvoie la sous-requête des identifiants sélectionnés.
func (sel Selection) sql() (string, []any, error) {
	if sel.Filter != nil {
		where, args := sel.Filter.where()
		return `SELECT t.id FROM transactions t` + where, args, nil
	}
	if len(sel.IDs) == 0 {
		return "", nil, validation.Error("Sélectionnez au moins une opération.")
	}
	ids, err := json.Marshal(sel.IDs)
	return `SELECT value FROM json_each(?)`, []any{string(ids)}, err
}
