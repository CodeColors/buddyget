// Package recurring gère les opérations récurrentes : règles saisies ou
// validées après détection dans l'historique. Seules les règles actives
// alimenteront le prévisionnel.
package recurring

import (
	"context"
	"database/sql"
	"errors"
	"iter"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/platform/db"
	"github.com/codecolors/buddyget/internal/platform/money"
	"github.com/codecolors/buddyget/internal/platform/validation"
)

const labelMaxLen = 200

// ErrNotFound signale une récurrence inexistante.
var ErrNotFound = errors.New("récurrence introuvable")

// Origin indique d'où vient une récurrence.
type Origin string

const (
	Manual   Origin = "manuelle"
	Detected Origin = "detectee"
)

// Rule est une récurrence, avec les noms utiles à l'affichage.
type Rule struct {
	ID            int64
	Label         string
	AccountID     int64
	AccountName   string
	Amount        int64
	Estimated     bool
	Period        calendar.Period
	LastDay       bool
	Shift         calendar.Shift // report d'une échéance tombant un jour non ouvré
	Start         calendar.Date
	End           calendar.Date // vide : sans fin
	CategoryID    int64
	CategoryName  string
	CategoryColor string
	Origin        Origin
	Active        bool
	Signature     string // série d'opérations que la récurrence représente
}

// Occurrence est une échéance : sa date théorique, qui identifie
// l'échéance, et sa date effective, reportée au jour ouvré voisin.
type Occurrence struct {
	Nominal calendar.Date
	Due     calendar.Date
}

// Occurrences parcourt dans l'ordre les échéances dont la date effective
// est postérieure ou égale à from, jusqu'à la date de fin : sans date de
// fin, le parcours ne s'arrête qu'à l'initiative de l'appelant.
func (r Rule) Occurrences(from calendar.Date) iter.Seq[Occurrence] {
	return func(yield func(Occurrence) bool) {
		_, n := r.Period.NextOnOrAfter(r.Start, r.LastDay, from.AddDays(-calendar.MaxShift))
		for ; ; n++ {
			d := r.Period.Nth(r.Start, n, r.LastDay)
			if r.End != "" && d > r.End {
				return
			}
			if o := (Occurrence{Nominal: d, Due: r.Shift.Apply(d)}); o.Due >= from && !yield(o) {
				return
			}
		}
	}
}

// Next renvoie la prochaine échéance à partir de from (incluse) ; faux si
// la récurrence est terminée.
func (r Rule) Next(from calendar.Date) (Occurrence, bool) {
	for o := range r.Occurrences(from) {
		return o, true
	}
	return Occurrence{}, false
}

// RuleInput est la saisie d'une récurrence ; Amount est signé.
type RuleInput struct {
	Label      string
	AccountID  int64
	Amount     int64
	Estimated  bool
	Period     calendar.Period
	LastDay    bool
	Shift      calendar.Shift
	Start      calendar.Date
	End        calendar.Date
	CategoryID int64
	Origin     Origin
	// Signature de la série détectée d'origine ; vide pour une saisie, elle
	// est alors tirée du libellé. Fixée à la création, elle empêche la
	// série d'être proposée de nouveau même si le libellé est modifié.
	Signature string
}

// Service gère les récurrences.
type Service struct {
	db *db.DB
}

// NewService crée le service des récurrences.
func NewService(d *db.DB) *Service { return &Service{db: d} }

const ruleSelect = `
	SELECT r.id, r.label, r.account_id, a.name, r.amount, r.estimated, r.period_count, r.period_unit, r.last_day,
		r.business_day, r.start_date, coalesce(r.end_date, ''), coalesce(r.category_id, 0),
		coalesce(CASE WHEN p.id IS NULL THEN c.name ELSE p.name || ' › ' || c.name END, ''), coalesce(c.color, ''),
		r.origin, r.active, r.signature
	FROM recurring_rules r
	JOIN accounts a ON a.id = r.account_id
	LEFT JOIN categories c ON c.id = r.category_id
	LEFT JOIN categories p ON p.id = c.parent_id`

func scanRule(row interface{ Scan(...any) error }) (Rule, error) {
	var r Rule
	err := row.Scan(&r.ID, &r.Label, &r.AccountID, &r.AccountName, &r.Amount, &r.Estimated, &r.Period.Count, &r.Period.Unit,
		&r.LastDay, &r.Shift, &r.Start, &r.End, &r.CategoryID, &r.CategoryName, &r.CategoryColor, &r.Origin, &r.Active, &r.Signature)
	return r, err
}

// Rules renvoie les récurrences, actives d'abord, par libellé.
func (s *Service) Rules(ctx context.Context) ([]Rule, error) {
	rows, err := s.db.R.QueryContext(ctx, ruleSelect+` ORDER BY r.active DESC, r.label COLLATE NOCASE, r.id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Rule
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Rule renvoie une récurrence.
func (s *Service) Rule(ctx context.Context, id int64) (Rule, error) {
	r, err := scanRule(s.db.R.QueryRowContext(ctx, ruleSelect+` WHERE r.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Rule{}, ErrNotFound
	}
	return r, err
}

// CreateRule crée une récurrence et renvoie son identifiant.
func (s *Service) CreateRule(ctx context.Context, in RuleInput) (int64, error) {
	if !slices.Contains([]Origin{Manual, Detected}, in.Origin) {
		return 0, errors.New("origine de récurrence inconnue")
	}
	in, err := s.validate(ctx, in)
	if err != nil {
		return 0, err
	}
	if in.Signature == "" {
		in.Signature = Signature(in.AccountID, in.Amount, in.Label)
	}
	res, err := s.db.W.ExecContext(ctx, `
		INSERT INTO recurring_rules (label, account_id, amount, estimated, period_count, period_unit, last_day,
			business_day, start_date, end_date, category_id, origin, signature)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, nullif(?, ''), nullif(?, 0), ?, ?)`,
		in.Label, in.AccountID, in.Amount, in.Estimated, in.Period.Count, in.Period.Unit, in.LastDay,
		in.Shift, in.Start, in.End, in.CategoryID, in.Origin, in.Signature)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateRule modifie une récurrence ; son origine et sa signature sont
// conservées (in.Origin et in.Signature sont ignorés).
func (s *Service) UpdateRule(ctx context.Context, id int64, in RuleInput) error {
	in, err := s.validate(ctx, in)
	if err != nil {
		return err
	}
	res, err := s.db.W.ExecContext(ctx, `
		UPDATE recurring_rules SET label = ?, account_id = ?, amount = ?, estimated = ?, period_count = ?, period_unit = ?,
			last_day = ?, business_day = ?, start_date = ?, end_date = nullif(?, ''), category_id = nullif(?, 0)
		WHERE id = ?`,
		in.Label, in.AccountID, in.Amount, in.Estimated, in.Period.Count, in.Period.Unit, in.LastDay,
		in.Shift, in.Start, in.End, in.CategoryID, id)
	return affectedOne(res, err)
}

// SetActive suspend ou réactive une récurrence.
func (s *Service) SetActive(ctx context.Context, id int64, active bool) error {
	res, err := s.db.W.ExecContext(ctx, `UPDATE recurring_rules SET active = ? WHERE id = ?`, active, id)
	return affectedOne(res, err)
}

// DeleteRule supprime une récurrence.
func (s *Service) DeleteRule(ctx context.Context, id int64) error {
	res, err := s.db.W.ExecContext(ctx, `DELETE FROM recurring_rules WHERE id = ?`, id)
	return affectedOne(res, err)
}

func affectedOne(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return errors.Join(err, ErrNotFound)
	}
	return nil
}

func (s *Service) validate(ctx context.Context, in RuleInput) (RuleInput, error) {
	in.Label = strings.TrimSpace(in.Label)
	switch {
	case in.Label == "":
		return in, validation.Error("Indiquez un libellé.")
	case utf8.RuneCountInString(in.Label) > labelMaxLen:
		return in, validation.Errorf("Le libellé ne doit pas dépasser %d caractères.", labelMaxLen)
	case in.Amount == 0:
		return in, validation.Error("Indiquez un montant non nul.")
	case in.Amount > money.Max || in.Amount < -money.Max:
		return in, validation.Error("Montant trop élevé.")
	case !in.Period.Valid():
		return in, validation.Error("Périodicité invalide : deux ans au plus entre deux échéances.")
	case !slices.Contains(calendar.Shifts, in.Shift):
		return in, validation.Error("Choisissez le report des échéances tombant un jour non ouvré.")
	case in.Start == "":
		return in, validation.Error("Indiquez la date de la première échéance.")
	case in.End != "" && in.End < in.Start:
		return in, validation.Error("La date de fin précède la première échéance.")
	}
	if in.Period.Unit != calendar.Month {
		in.LastDay = false
	}
	var ok bool
	if err := s.db.R.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM accounts WHERE id = ?)`, in.AccountID).Scan(&ok); err != nil {
		return in, err
	}
	if !ok {
		return in, validation.Error("Choisissez un compte.")
	}
	if in.CategoryID != 0 {
		if err := s.db.R.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM categories WHERE id = ?)`, in.CategoryID).Scan(&ok); err != nil {
			return in, err
		}
		if !ok {
			return in, validation.Error("Catégorie introuvable.")
		}
	}
	return in, nil
}

// Detected renvoie les séries détectées dans l'historique, sauf celles déjà
// couvertes par une récurrence ou rejetées.
func (s *Service) Detected(ctx context.Context, today calendar.Date) ([]Candidate, error) {
	ops, err := s.history(ctx, today)
	if err != nil {
		return nil, err
	}
	known, err := s.knownSignatures(ctx)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(Detect(ops, today), func(c Candidate) bool { return known[c.Signature] }), nil
}

// Candidate renvoie une série détectée par sa signature.
func (s *Service) Candidate(ctx context.Context, signature string, today calendar.Date) (Candidate, error) {
	candidates, err := s.Detected(ctx, today)
	if err != nil {
		return Candidate{}, err
	}
	i := slices.IndexFunc(candidates, func(c Candidate) bool { return c.Signature == signature })
	if i < 0 {
		return Candidate{}, ErrNotFound
	}
	return candidates[i], nil
}

// Accept transforme une série détectée en récurrence, telle quelle.
func (s *Service) Accept(ctx context.Context, signature string, today calendar.Date) (int64, error) {
	c, err := s.Candidate(ctx, signature, today)
	if err != nil {
		return 0, err
	}
	return s.CreateRule(ctx, c.Input())
}

// Input prépare la saisie d'une récurrence à partir d'une série détectée :
// elle part de la dernière occurrence constatée.
func (c Candidate) Input() RuleInput {
	return RuleInput{
		Label: c.Label, AccountID: c.AccountID, Amount: c.Amount, Estimated: c.Estimated,
		Period: c.Period, LastDay: c.LastDay, Shift: calendar.NextBusinessDay, Start: c.Last, CategoryID: c.CategoryID, Origin: Detected,
		Signature: c.Signature,
	}
}

// Dismiss rejette une série : elle ne sera plus proposée.
func (s *Service) Dismiss(ctx context.Context, signature string) error {
	_, err := s.db.W.ExecContext(ctx, `INSERT OR IGNORE INTO recurring_dismissals (signature) VALUES (?)`, signature)
	return err
}

// history lit les opérations des dernières années, hors virements internes.
func (s *Service) history(ctx context.Context, today calendar.Date) ([]Op, error) {
	since := calendar.FromTime(today.Time().AddDate(-historyYears, 0, 0))
	rows, err := s.db.R.QueryContext(ctx, `
		SELECT account_id, date, label, amount, coalesce(category_id, 0) FROM transactions
		WHERE transfer_id IS NULL AND date >= ? AND date <= ?`, since, today)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ops []Op
	for rows.Next() {
		var o Op
		if err := rows.Scan(&o.AccountID, &o.Date, &o.Label, &o.Amount, &o.CategoryID); err != nil {
			return nil, err
		}
		ops = append(ops, o)
	}
	return ops, rows.Err()
}

func (s *Service) knownSignatures(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.R.QueryContext(ctx, `
		SELECT signature FROM recurring_rules UNION SELECT signature FROM recurring_dismissals`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	known := map[string]bool{}
	for rows.Next() {
		var sig string
		if err := rows.Scan(&sig); err != nil {
			return nil, err
		}
		known[sig] = true
	}
	return known, rows.Err()
}
