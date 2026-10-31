package savings

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/platform/db"
	"github.com/codecolors/buddyget/internal/platform/validation"
)

// Service gère les comptes d'épargne.
type Service struct {
	db     *db.DB
	ledger *ledger.Service
}

// NewService crée le service de l'épargne.
func NewService(d *db.DB, l *ledger.Service) *Service { return &Service{db: d, ledger: l} }

const settingsSelect = `
	SELECT account_id, product, rate, method, auto_interest, ceiling, coalesce(unlock_date, ''), lock_months, penalty, since
	FROM savings_settings`

func scanSettings(row interface{ Scan(...any) error }) (Settings, error) {
	var st Settings
	err := row.Scan(&st.AccountID, &st.Product, &st.Rate, &st.Method, &st.AutoInterest, &st.Ceiling,
		&st.UnlockDate, &st.LockMonths, &st.Penalty, &st.Since)
	return st, err
}

// Settings renvoie les réglages d'un compte ; faux s'il n'en a pas.
func (s *Service) Settings(ctx context.Context, accountID int64) (Settings, bool, error) {
	st, err := scanSettings(s.db.R.QueryRowContext(ctx, settingsSelect+` WHERE account_id = ?`, accountID))
	if errors.Is(err, sql.ErrNoRows) {
		return Settings{}, false, nil
	}
	return st, err == nil, err
}

// AllSettings renvoie les réglages de tous les comptes d'épargne.
func (s *Service) AllSettings(ctx context.Context) (map[int64]Settings, error) {
	rows, err := s.db.R.QueryContext(ctx, settingsSelect)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[int64]Settings{}
	for rows.Next() {
		st, err := scanSettings(rows)
		if err != nil {
			return nil, err
		}
		out[st.AccountID] = st
	}
	return out, rows.Err()
}

// Save enregistre les réglages d'un compte d'épargne ; la date du premier
// enregistrement est conservée.
func (s *Service) Save(ctx context.Context, st Settings, today calendar.Date) error {
	if err := st.Validate(); err != nil {
		return err
	}
	a, err := s.ledger.Account(ctx, st.AccountID)
	if err != nil {
		return err
	}
	if a.Kind != ledger.AccountSavings {
		return validation.Error("Seul un compte d'épargne a des réglages d'épargne.")
	}
	_, err = s.db.W.ExecContext(ctx, `
		INSERT INTO savings_settings (account_id, product, rate, method, auto_interest, ceiling, unlock_date, lock_months, penalty, since)
		VALUES (?, ?, ?, ?, ?, ?, nullif(?, ''), ?, ?, ?)
		ON CONFLICT (account_id) DO UPDATE SET product = excluded.product, rate = excluded.rate, method = excluded.method,
			auto_interest = excluded.auto_interest, ceiling = excluded.ceiling, unlock_date = excluded.unlock_date,
			lock_months = excluded.lock_months, penalty = excluded.penalty`,
		st.AccountID, st.Product, st.Rate, st.Method, st.AutoInterest, st.Ceiling, st.UnlockDate, st.LockMonths, st.Penalty, today)
	return err
}

// Remove supprime les réglages d'un compte qui n'est plus un compte
// d'épargne.
func (s *Service) Remove(ctx context.Context, accountID int64) error {
	_, err := s.db.W.ExecContext(ctx, `DELETE FROM savings_settings WHERE account_id = ?`, accountID)
	return err
}

// Availability renvoie les fonds disponibles et bloqués d'un compte au soir
// du jour on, et ses réglages ; faux pour un compte sans réglages d'épargne.
func (s *Service) Availability(ctx context.Context, accountID int64, on calendar.Date) (Availability, Settings, bool, error) {
	st, ok, err := s.Settings(ctx, accountID)
	if err != nil || !ok {
		return Availability{}, st, false, err
	}
	moves, err := s.Moves(ctx, accountID)
	return AvailabilityOn(st, moves, on), st, true, err
}

// YearInterest est l'intérêt d'une année.
type YearInterest struct {
	Year   int
	Amount int64
}

// Summary résume un compte d'épargne pour la vue Épargne.
type Summary struct {
	Account      ledger.AccountBalance
	Settings     Settings
	Configured   bool // réglages d'épargne enregistrés
	Availability Availability
	InterestPaid int64          // intérêts inscrits depuis l'ouverture
	Accrued      int64          // intérêts acquis cette année à ce jour
	YearEstimate int64          // intérêts de l'année au 31/12, sans autre mouvement
	Missing      []YearInterest // années closes sans intérêts inscrits
}

// CeilingRoom renvoie ce qui peut encore être versé sous le plafond
// (négatif s'il est dépassé) ; sans objet sans plafond.
func (s Summary) CeilingRoom() int64 { return s.Settings.Ceiling - s.Account.Balance }

// Overview résume les comptes d'épargne actifs au jour today.
func (s *Service) Overview(ctx context.Context, today calendar.Date) ([]Summary, error) {
	accounts, err := s.ledger.Accounts(ctx, today)
	if err != nil {
		return nil, err
	}
	all, err := s.AllSettings(ctx)
	if err != nil {
		return nil, err
	}
	recorded, err := s.recordedInterest(ctx)
	if err != nil {
		return nil, err
	}
	year := today.Time().Year()
	var out []Summary
	for _, a := range accounts {
		if a.Archived || a.Kind != ledger.AccountSavings {
			continue
		}
		sum := Summary{Account: a}
		sum.Settings, sum.Configured = all[a.ID]
		moves, err := s.Moves(ctx, a.ID)
		if err != nil {
			return nil, err
		}
		sum.Availability = AvailabilityOn(sum.Settings, moves, today)
		for _, amount := range recorded[a.ID] {
			sum.InterestPaid += amount
		}
		if sum.Settings.EarnsInterest() {
			start, inYear := yearStart(moves, year)
			sum.Accrued = Interest(sum.Settings, start, inYear, today)
			sum.YearEstimate = Interest(sum.Settings, start, inYear, yearEnd(year))
			for y := a.OpeningDate.Time().Year(); y < year; y++ {
				if _, ok := recorded[a.ID][y]; !ok {
					start, inYear := yearStart(moves, y)
					if amount := Interest(sum.Settings, start, inYear, yearEnd(y)); amount > 0 {
						sum.Missing = append(sum.Missing, YearInterest{y, amount})
					}
				}
			}
		}
		out = append(out, sum)
	}
	return out, nil
}

// RecordInterest inscrit les intérêts d'une année close : une opération
// datée du 31 décembre, modifiable ensuite comme toute autre.
func (s *Service) RecordInterest(ctx context.Context, accountID int64, year int, today calendar.Date) error {
	if yearEnd(year) > today {
		return validation.Error("Les intérêts d'une année s'inscrivent une fois l'année terminée.")
	}
	st, ok, err := s.Settings(ctx, accountID)
	if err != nil {
		return err
	}
	if !ok || !st.EarnsInterest() {
		return validation.Error("Ce compte n'est pas rémunéré : indiquez un taux et une méthode de calcul.")
	}
	moves, err := s.Moves(ctx, accountID)
	if err != nil {
		return err
	}
	start, inYear := yearStart(moves, year)
	amount := Interest(st, start, inYear, yearEnd(year))
	if amount <= 0 {
		return validation.Errorf("Aucun intérêt calculé pour %d.", year)
	}
	_, err = s.ledger.CreateTransaction(ctx, ledger.TransactionInput{
		AccountID: accountID, Date: yearEnd(year), Label: fmt.Sprintf("Intérêts %d", year), Amount: amount, InterestYear: year,
	})
	return err
}

// CloseYears inscrit les intérêts des années closes des comptes réglés en
// inscription automatique, à partir de l'année où leurs réglages ont été
// enregistrés. Renvoie le nombre d'écritures créées.
func (s *Service) CloseYears(ctx context.Context, today calendar.Date) (int, error) {
	all, err := s.AllSettings(ctx)
	if err != nil {
		return 0, err
	}
	recorded, err := s.recordedInterest(ctx)
	if err != nil {
		return 0, err
	}
	created := 0
	for id, st := range all {
		if !st.AutoInterest || !st.EarnsInterest() {
			continue
		}
		for y := st.Since.Time().Year(); yearEnd(y) < today; y++ {
			if _, ok := recorded[id][y]; ok {
				continue
			}
			err := s.RecordInterest(ctx, id, y, today)
			if _, input := validation.Message(err); input {
				continue // rien à inscrire cette année-là
			}
			if err != nil {
				return created, err
			}
			created++
		}
	}
	return created, nil
}

// recordedInterest renvoie les intérêts inscrits, par compte et par année.
func (s *Service) recordedInterest(ctx context.Context) (map[int64]map[int]int64, error) {
	rows, err := s.db.R.QueryContext(ctx, `SELECT account_id, interest_year, amount FROM transactions WHERE interest_year IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[int64]map[int]int64{}
	for rows.Next() {
		var id, amount int64
		var year int
		if err := rows.Scan(&id, &year, &amount); err != nil {
			return nil, err
		}
		if out[id] == nil {
			out[id] = map[int]int64{}
		}
		out[id][year] = amount
	}
	return out, rows.Err()
}

// Moves lit les mouvements d'un compte : le solde initial, puis les
// opérations, dans l'ordre chronologique. Le solde initial est le solde au
// début du jour d'ouverture : il figure comme un mouvement de la veille, et
// rapporte donc dès ce jour-là.
func (s *Service) Moves(ctx context.Context, accountID int64) ([]Move, error) {
	a, err := s.ledger.Account(ctx, accountID)
	if err != nil {
		return nil, err
	}
	moves := []Move{{Date: a.OpeningDate.AddDays(-1), Amount: a.OpeningBalance}}
	rows, err := s.db.R.QueryContext(ctx, `
		SELECT date, amount, interest_year IS NOT NULL FROM transactions
		WHERE account_id = ? AND date >= ? ORDER BY date, id`, accountID, a.OpeningDate)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var m Move
		if err := rows.Scan(&m.Date, &m.Amount, &m.Interest); err != nil {
			return nil, err
		}
		moves = append(moves, m)
	}
	return moves, rows.Err()
}

// yearStart sépare l'état du compte au 1er janvier de year et les
// mouvements de l'année.
func yearStart(moves []Move, year int) (Start, []Move) {
	var start Start
	var in []Move
	for _, m := range moves {
		switch y := m.Date.Time().Year(); {
		case y < year:
			start.Balance += m.Amount
			if m.Interest {
				start.Interest += m.Amount
			}
		case y == year:
			in = append(in, m)
		}
	}
	return start, in
}

// yearEnd renvoie le 31 décembre d'une année.
func yearEnd(year int) calendar.Date {
	return calendar.FromTime(time.Date(year, time.December, 31, 0, 0, 0, 0, time.UTC))
}

// Balance est un solde à une date.
type Balance struct {
	Date   calendar.Date
	Amount int64
}

// History renvoie le total des comptes d'épargne actifs à la fin de chacun
// des derniers mois (months au plus, depuis la première ouverture), puis
// au jour today.
func (s *Service) History(ctx context.Context, today calendar.Date, months int) ([]Balance, error) {
	// Variation du total par mois (AAAA-MM) : soldes initiaux et opérations.
	rows, err := s.db.R.QueryContext(ctx, `
		SELECT substr(opening_date, 1, 7), sum(opening_balance) FROM accounts
		WHERE kind = 'epargne' AND NOT archived AND opening_date <= ?1 GROUP BY 1
		UNION ALL
		SELECT substr(t.date, 1, 7), sum(t.amount) FROM transactions t JOIN accounts a ON a.id = t.account_id
		WHERE a.kind = 'epargne' AND NOT a.archived AND t.date >= a.opening_date AND t.date <= ?1 GROUP BY 1`, today)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	deltas := map[string]int64{}
	first := ""
	for rows.Next() {
		var month string
		var sum int64
		if err := rows.Scan(&month, &sum); err != nil {
			return nil, err
		}
		deltas[month] += sum
		if first == "" || month < first {
			first = month
		}
	}
	if err := rows.Err(); err != nil || first == "" {
		return nil, err
	}
	t := today.Time()
	current := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	keep := current.AddDate(0, -months, 0)
	var out []Balance
	var total int64
	for m, _ := time.Parse("2006-01", first); !m.After(current); m = m.AddDate(0, 1, 0) {
		total += deltas[m.Format("2006-01")]
		end := calendar.FromTime(m.AddDate(0, 1, -1))
		if m.Equal(current) {
			end = today
		}
		if !m.Before(keep) {
			out = append(out, Balance{end, total})
		}
	}
	return out, nil
}
