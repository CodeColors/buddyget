package ledger

import (
	"context"
	"database/sql"
	"errors"

	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/platform/money"
	"github.com/codecolors/buddyget/internal/platform/validation"
)

// Reconciliation compare le solde affiché par la banque au solde calculé à
// la même date. C'est l'unique mécanisme de rapprochement : la vue de
// pointage s'appuiera sur le même solde bancaire.
type Reconciliation struct {
	Account Account
	// HasBankBalance est faux tant qu'aucun solde bancaire n'a été saisi.
	HasBankBalance bool
	BankBalance    int64
	BankDate       calendar.Date
	Computed       int64 // solde calculé au soir de BankDate
	Uncleared      int   // opérations non pointées jusqu'à BankDate
}

// Gap renvoie l'écart entre la banque et BuddyGet : positif quand la banque
// affiche davantage.
func (r Reconciliation) Gap() int64 { return r.BankBalance - r.Computed }

// SetBankBalance enregistre le solde relevé sur le site de la banque.
func (s *Service) SetBankBalance(ctx context.Context, accountID, balance int64, on calendar.Date) error {
	if balance > money.Max || balance < -money.Max {
		return validation.Error("Solde trop élevé.")
	}
	if on == "" {
		return validation.Error("Indiquez la date du solde.")
	}
	res, err := s.db.W.ExecContext(ctx,
		`UPDATE accounts SET bank_balance = ?, bank_balance_date = ? WHERE id = ?`, balance, on, accountID)
	return affectedOne(res, err)
}

// Reconciliation renvoie l'état du rapprochement d'un compte.
func (s *Service) Reconciliation(ctx context.Context, accountID int64) (Reconciliation, error) {
	var r Reconciliation
	var bank sql.NullInt64
	var bankDate sql.NullString
	a, err := scanAccount(s.db.R.QueryRowContext(ctx,
		`SELECT `+accountColumns+`, a.bank_balance, a.bank_balance_date FROM accounts a WHERE a.id = ?`, accountID),
		&bank, &bankDate)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	if err != nil {
		return r, err
	}
	r.Account = a
	if !bank.Valid {
		return r, nil
	}
	r.HasBankBalance, r.BankBalance, r.BankDate = true, bank.Int64, calendar.Date(bankDate.String)
	err = s.db.R.QueryRowContext(ctx, `
		SELECT ? + coalesce(sum(amount) FILTER (WHERE date >= ?), 0), count(*) FILTER (WHERE cleared = 0)
		FROM transactions WHERE account_id = ? AND date <= ?`,
		a.OpeningBalance, a.OpeningDate, accountID, r.BankDate,
	).Scan(&r.Computed, &r.Uncleared)
	return r, err
}
