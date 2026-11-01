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
	Cleared        int64 // solde des seules opérations pointées au soir de BankDate
	Uncleared      int   // opérations non pointées, du solde initial à BankDate
}

// Gap renvoie l'écart entre la banque et BuddyGet : positif quand la banque
// affiche davantage.
func (r Reconciliation) Gap() int64 { return r.BankBalance - r.Computed }

// ClearedGap renvoie l'écart entre la banque et le solde pointé : il tombe
// à zéro quand toutes les opérations vues sur le relevé sont pointées.
func (r Reconciliation) ClearedGap() int64 { return r.BankBalance - r.Cleared }

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
		SELECT ?1 + coalesce(sum(amount) FILTER (WHERE date >= ?2), 0), count(*) FILTER (WHERE cleared = 0 AND date >= ?2),
			?1 + coalesce(sum(amount) FILTER (WHERE date >= ?2 AND cleared), 0)
		FROM transactions WHERE account_id = ?3 AND date <= ?4`,
		a.OpeningBalance, a.OpeningDate, accountID, r.BankDate,
	).Scan(&r.Computed, &r.Uncleared, &r.Cleared)
	return r, err
}

// Uncleared totalise les opérations non pointées d'un compte.
type Uncleared struct {
	Count int
	Sum   int64
}

// UnclearedByAccount totalise, par compte, les opérations non pointées
// comptées dans le solde au soir du jour on : retranchées de ce solde, elles
// donnent le solde pointé.
func (s *Service) UnclearedByAccount(ctx context.Context, on calendar.Date) (map[int64]Uncleared, error) {
	rows, err := s.db.R.QueryContext(ctx, `
		SELECT t.account_id, count(*), sum(t.amount) FROM transactions t JOIN accounts a ON a.id = t.account_id
		WHERE t.cleared = 0 AND t.date >= a.opening_date AND t.date <= ?
		GROUP BY t.account_id`, on)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[int64]Uncleared{}
	for rows.Next() {
		var id int64
		var u Uncleared
		if err := rows.Scan(&id, &u.Count, &u.Sum); err != nil {
			return nil, err
		}
		out[id] = u
	}
	return out, rows.Err()
}
