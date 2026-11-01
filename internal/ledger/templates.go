package ledger

import (
	"context"
	"database/sql"
	"errors"

	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/platform/db"
	"github.com/codecolors/buddyget/internal/platform/validation"
)

const templateNameMaxLen = 40

// QuickTemplate est un modèle de saisie fréquente : un clic crée
// l'opération du jour.
type QuickTemplate struct {
	ID          int64
	Name        string
	AccountID   int64
	AccountName string
	Label       string
	Amount      int64 // signé
	CategoryID  int64
}

// Templates renvoie les modèles de saisie, par nom.
func (s *Service) Templates(ctx context.Context) ([]QuickTemplate, error) {
	rows, err := s.db.R.QueryContext(ctx, `
		SELECT q.id, q.name, q.account_id, a.name, q.label, q.amount, coalesce(q.category_id, 0)
		FROM quick_templates q JOIN accounts a ON a.id = q.account_id
		WHERE NOT a.archived ORDER BY q.name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []QuickTemplate
	for rows.Next() {
		var q QuickTemplate
		if err := rows.Scan(&q.ID, &q.Name, &q.AccountID, &q.AccountName, &q.Label, &q.Amount, &q.CategoryID); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// SaveAsTemplate crée un modèle à partir d'une opération simple.
func (s *Service) SaveAsTemplate(ctx context.Context, transactionID int64, name string) (int64, error) {
	t, err := s.Transaction(ctx, transactionID)
	if err != nil {
		return 0, err
	}
	if t.IsTransfer() {
		return 0, validation.Error("Un virement ne peut pas servir de modèle.")
	}
	if name, err = cleanText(name, "le nom du modèle", templateNameMaxLen, true); err != nil {
		return 0, err
	}
	res, err := s.db.W.ExecContext(ctx, `
		INSERT INTO quick_templates (name, account_id, label, amount, category_id) VALUES (?, ?, ?, ?, nullif(?, 0))`,
		name, t.AccountID, t.Label, t.Amount, t.CategoryID)
	if db.IsUniqueViolation(err) {
		return 0, validation.Error("Un modèle porte déjà ce nom.")
	}
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// DeleteTemplate supprime un modèle.
func (s *Service) DeleteTemplate(ctx context.Context, id int64) error {
	res, err := s.db.W.ExecContext(ctx, `DELETE FROM quick_templates WHERE id = ?`, id)
	return affectedOne(res, err)
}

// UseTemplate crée l'opération d'un modèle au jour on et renvoie son
// identifiant.
func (s *Service) UseTemplate(ctx context.Context, id int64, on calendar.Date) (int64, error) {
	var q QuickTemplate
	err := s.db.R.QueryRowContext(ctx, `SELECT account_id, label, amount, coalesce(category_id, 0) FROM quick_templates WHERE id = ?`, id).
		Scan(&q.AccountID, &q.Label, &q.Amount, &q.CategoryID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	return s.CreateTransaction(ctx, TransactionInput{AccountID: q.AccountID, Date: on, Label: q.Label, Amount: q.Amount, CategoryID: q.CategoryID})
}
