package forecast

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/platform/db"
	"github.com/codecolors/buddyget/internal/platform/money"
	"github.com/codecolors/buddyget/internal/platform/validation"
	"github.com/codecolors/buddyget/internal/recurring"
	"github.com/codecolors/buddyget/internal/savings"
)

// MaxMonths borne l'horizon du prévisionnel.
const MaxMonths = 24

// AverageChoices sont les profondeurs proposées pour la moyenne glissante
// des dépenses variables, en mois ; 0 désactive l'estimation.
var AverageChoices = []int{0, 3, 6, 12}

// Settings sont les réglages du prévisionnel, communs au foyer.
type Settings struct {
	Threshold     int64 // seuil d'alerte du solde
	ClearedOnly   bool  // solde de départ limité aux opérations pointées
	AverageMonths int   // profondeur de la moyenne glissante (0 : sans)
}

// Query décrit le prévisionnel demandé : un compte, sinon tous les comptes
// d'épargne actifs (Savings), sinon tous les comptes courants actifs.
type Query struct {
	AccountID int64
	Savings   bool
	Target    calendar.Date
}

// CategoryAverage est la dépense variable mensuelle moyenne d'une catégorie.
type CategoryAverage struct {
	Name    string // vide : sans catégorie
	Color   string
	Monthly int64
}

// Report est un prévisionnel prêt à afficher.
type Report struct {
	Result
	Settings Settings
	Accounts []ledger.AccountBalance // comptes couverts
	// Mois complets réellement moyennés pour les dépenses variables : moins
	// que demandé quand l'historique est plus court, 0 sans estimation.
	AverageMonths int
	Categories    []CategoryAverage // des plus fortes dépenses aux plus faibles
}

// VariableMonthly renvoie l'estimation mensuelle des dépenses variables.
func (r Report) VariableMonthly() int64 {
	var total int64
	for _, c := range r.Categories {
		total += c.Monthly
	}
	return total
}

// Service calcule les prévisionnels.
type Service struct {
	db        *db.DB
	ledger    *ledger.Service
	recurring *recurring.Service
	savings   *savings.Service
}

// NewService crée le service du prévisionnel.
func NewService(d *db.DB, l *ledger.Service, r *recurring.Service, sv *savings.Service) *Service {
	return &Service{db: d, ledger: l, recurring: r, savings: sv}
}

// MaxTarget renvoie la date cible la plus lointaine admise.
func MaxTarget(today calendar.Date) calendar.Date {
	return calendar.FromTime(today.Time().AddDate(0, MaxMonths, 0))
}

// Settings renvoie les réglages du prévisionnel.
func (s *Service) Settings(ctx context.Context) (Settings, error) {
	var st Settings
	err := s.db.R.QueryRowContext(ctx, `SELECT threshold, cleared_only, average_months FROM forecast_settings`).
		Scan(&st.Threshold, &st.ClearedOnly, &st.AverageMonths)
	return st, err
}

// SaveSettings enregistre les réglages du prévisionnel.
func (s *Service) SaveSettings(ctx context.Context, st Settings) error {
	if st.Threshold > money.Max || st.Threshold < -money.Max {
		return validation.Error("Seuil trop élevé.")
	}
	if !slices.Contains(AverageChoices, st.AverageMonths) {
		return validation.Error("Choisissez la période de la moyenne des dépenses variables.")
	}
	_, err := s.db.W.ExecContext(ctx, `UPDATE forecast_settings SET threshold = ?, cleared_only = ?, average_months = ?`,
		st.Threshold, st.ClearedOnly, st.AverageMonths)
	return err
}

// SetExcluded écarte une échéance du prévisionnel, ou l'y réintègre ;
// nominal est sa date théorique. Les exclusions d'échéances passées sont
// purgées au passage.
func (s *Service) SetExcluded(ctx context.Context, ruleID int64, nominal calendar.Date, excluded bool, today calendar.Date) error {
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM forecast_exclusions WHERE date < ?`, today.AddDays(-calendar.MaxShift)); err != nil {
			return err
		}
		if !excluded {
			_, err := tx.ExecContext(ctx, `DELETE FROM forecast_exclusions WHERE rule_id = ? AND date = ?`, ruleID, nominal)
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO forecast_exclusions (rule_id, date) VALUES (?, ?)`, ruleID, nominal)
		if db.IsForeignKeyViolation(err) {
			return recurring.ErrNotFound
		}
		return err
	})
}

// Forecast calcule le prévisionnel au jour today.
func (s *Service) Forecast(ctx context.Context, q Query, today calendar.Date) (Report, error) {
	switch {
	case q.Target <= today:
		return Report{}, validation.Error("Choisissez une date à venir.")
	case q.Target > MaxTarget(today):
		return Report{}, validation.Errorf("Le prévisionnel porte sur %d mois au plus.", MaxMonths)
	}
	var r Report
	var err error
	if r.Settings, err = s.Settings(ctx); err != nil {
		return r, err
	}
	if err := s.selectAccounts(ctx, &r, q, today); err != nil {
		return r, err
	}
	names := map[int64]string{}
	in := Input{Today: today, Target: q.Target, Threshold: r.Settings.Threshold}
	for _, a := range r.Accounts {
		names[a.ID] = a.Name
		in.Opening += a.Balance
	}
	if r.Settings.ClearedOnly {
		if err := s.pending(ctx, &in, names); err != nil {
			return r, err
		}
	}
	rules, err := s.recurring.Rules(ctx)
	if err != nil {
		return r, err
	}
	if err := s.occurrences(ctx, &in, rules, names); err != nil {
		return r, err
	}
	if err := s.planned(ctx, &in, names); err != nil {
		return r, err
	}
	if err := s.interest(ctx, &in, names); err != nil {
		return r, err
	}
	if in.Variable, err = s.variable(ctx, &r, rules, names, today); err != nil {
		return r, err
	}
	r.Result = Compute(in)
	return r, nil
}

// selectAccounts retient les comptes couverts par le prévisionnel.
func (s *Service) selectAccounts(ctx context.Context, r *Report, q Query, today calendar.Date) error {
	accounts, err := s.ledger.Accounts(ctx, today)
	if err != nil {
		return err
	}
	kind := ledger.AccountCurrent
	if q.Savings {
		kind = ledger.AccountSavings
	}
	for _, a := range accounts {
		if q.AccountID == a.ID || q.AccountID == 0 && !a.Archived && a.Kind == kind {
			r.Accounts = append(r.Accounts, a)
		}
	}
	switch {
	case len(r.Accounts) > 0:
		return nil
	case q.AccountID != 0:
		return ledger.ErrNotFound
	case q.Savings:
		return validation.Error("Aucun compte d'épargne : créez-en un ou choisissez un compte.")
	}
	return validation.Error("Aucun compte courant : choisissez un compte.")
}

// pending retire du solde de départ les opérations non pointées et les
// reporte au lendemain, une ligne par compte.
func (s *Service) pending(ctx context.Context, in *Input, names map[int64]string) error {
	uncleared, err := s.ledger.UnclearedByAccount(ctx, in.Today)
	if err != nil {
		return err
	}
	for id, name := range names {
		if u := uncleared[id]; u.Count > 0 {
			in.Opening -= u.Sum
			label := "1 opération non pointée"
			if u.Count > 1 {
				label = fmt.Sprintf("%d opérations non pointées", u.Count)
			}
			in.Events = append(in.Events, Event{Date: in.Today.AddDays(1), Label: label, Amount: u.Sum, Kind: Pending, AccountID: id, AccountName: name})
		}
	}
	return nil
}

// occurrences ajoute les échéances des récurrences actives des comptes
// couverts, jusqu'à la date cible.
func (s *Service) occurrences(ctx context.Context, in *Input, rules []recurring.Rule, names map[int64]string) error {
	type key struct {
		rule int64
		date calendar.Date
	}
	excluded := map[key]bool{}
	rows, err := s.db.R.QueryContext(ctx, `SELECT rule_id, date FROM forecast_exclusions`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var k key
		if err := rows.Scan(&k.rule, &k.date); err != nil {
			return err
		}
		excluded[k] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, rule := range rules {
		if !rule.Active {
			continue
		}
		for _, leg := range legs(rule, names) {
			for o := range rule.Occurrences(in.Today.AddDays(1)) {
				if o.Due > in.Target {
					break
				}
				e := leg
				e.Date, e.Nominal, e.Excluded = o.Due, o.Nominal, excluded[key{rule.ID, o.Nominal}]
				in.Events = append(in.Events, e)
			}
		}
	}
	return nil
}

// legs renvoie les mouvements d'une échéance sur les comptes couverts : un
// seul pour une dépense ou un revenu ; pour un versement programmé, le débit
// et le crédit, sauf s'ils s'annulent (deux comptes couverts).
func legs(rule recurring.Rule, names map[int64]string) []Event {
	base := Event{Label: rule.Label, Kind: Recurring, Estimated: rule.Estimated, RuleID: rule.ID}
	var out []Event
	from, fromOK := names[rule.AccountID]
	to, toOK := names[rule.ToAccountID]
	if fromOK && toOK {
		return nil
	}
	if fromOK {
		e := base
		e.Amount, e.AccountID, e.AccountName = rule.Amount, rule.AccountID, from
		out = append(out, e)
	}
	if toOK {
		e := base
		e.Amount, e.AccountID, e.AccountName = -rule.Amount, rule.ToAccountID, to
		out = append(out, e)
	}
	return out
}

// planned ajoute les opérations saisies à une date future, sauf les
// virements entre deux comptes couverts, sans effet sur leur total.
func (s *Service) planned(ctx context.Context, in *Input, names map[int64]string) error {
	ids, err := accountList(names)
	if err != nil {
		return err
	}
	rows, err := s.db.R.QueryContext(ctx, `
		WITH covered (id) AS (SELECT value FROM json_each(?))
		SELECT t.account_id, t.date, t.label, t.amount FROM transactions t
		WHERE t.account_id IN covered AND t.date > ? AND t.date <= ?
			AND NOT EXISTS (SELECT 1 FROM transactions o
				WHERE o.transfer_id = t.transfer_id AND o.id <> t.id AND o.account_id IN covered)`,
		ids, in.Today, in.Target)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		e := Event{Kind: Planned}
		var account int64
		if err := rows.Scan(&account, &e.Date, &e.Label, &e.Amount); err != nil {
			return err
		}
		e.AccountID, e.AccountName = account, names[account]
		in.Events = append(in.Events, e)
	}
	return rows.Err()
}

// interest ajoute les intérêts estimés des comptes d'épargne rémunérés,
// crédités chaque 31 décembre de l'horizon, d'après leurs mouvements passés
// et les mouvements prévus (hors échéances écartées).
func (s *Service) interest(ctx context.Context, in *Input, names map[int64]string) error {
	all, err := s.savings.AllSettings(ctx)
	if err != nil {
		return err
	}
	for id, name := range names {
		st, ok := all[id]
		if !ok || !st.EarnsInterest() {
			continue
		}
		moves, err := s.savings.Moves(ctx, id)
		if err != nil {
			return err
		}
		for _, e := range in.Events {
			if e.AccountID == id && !e.Excluded {
				moves = append(moves, savings.Move{Date: e.Date, Amount: e.Amount})
			}
		}
		for _, m := range savings.ProjectInterest(st, moves, in.Today, in.Target) {
			in.Events = append(in.Events, Event{
				Date: m.Date, Label: fmt.Sprintf("Intérêts %d", m.Date.Time().Year()), Amount: m.Amount,
				Kind: Interest, AccountID: id, AccountName: name, Estimated: true,
			})
		}
	}
	return nil
}

// variable estime les dépenses variables mensuelles : moyenne, par
// catégorie, des dépenses des derniers mois complets, hors virements
// internes et hors opérations d'une série couverte par une récurrence.
func (s *Service) variable(ctx context.Context, r *Report, rules []recurring.Rule, names map[int64]string, today calendar.Date) (int64, error) {
	if r.Settings.AverageMonths == 0 {
		return 0, nil
	}
	// L'historique commence à la date du solde initial du plus ancien compte.
	first := slices.MinFunc(r.Accounts, func(a, b ledger.AccountBalance) int {
		return strings.Compare(string(a.OpeningDate), string(b.OpeningDate))
	}).OpeningDate
	from, to, months := averageWindow(today, r.Settings.AverageMonths, first)
	if r.AverageMonths = months; months == 0 {
		return 0, nil
	}
	ids, err := accountList(names)
	if err != nil {
		return 0, err
	}
	recurrent := map[string]bool{}
	for _, rule := range rules {
		recurrent[rule.Signature] = true
	}
	rows, err := s.db.R.QueryContext(ctx, `
		SELECT account_id, label, amount, coalesce(category_id, 0) FROM transactions
		WHERE account_id IN (SELECT value FROM json_each(?)) AND transfer_id IS NULL AND amount < 0 AND date >= ? AND date <= ?`,
		ids, from, to)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	sums := map[int64]int64{}
	for rows.Next() {
		var account, amount, category int64
		var label string
		if err := rows.Scan(&account, &label, &amount, &category); err != nil {
			return 0, err
		}
		if !recurrent[recurring.Signature(account, amount, label)] {
			sums[category] += amount
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	return s.averages(ctx, r, sums, months)
}

// averages ramène les dépenses de chaque catégorie à une moyenne mensuelle
// et renvoie leur total.
func (s *Service) averages(ctx context.Context, r *Report, sums map[int64]int64, months int) (int64, error) {
	cats, err := s.ledger.Categories(ctx)
	if err != nil {
		return 0, err
	}
	byID := make(map[int64]ledger.Category, len(cats))
	for _, c := range cats {
		byID[c.ID] = c
	}
	for id, sum := range sums {
		avg := CategoryAverage{Monthly: int64(math.Round(float64(sum) / float64(months)))}
		if c, ok := byID[id]; ok {
			avg.Name, avg.Color = c.FullName(), c.Color
		}
		r.Categories = append(r.Categories, avg)
	}
	slices.SortFunc(r.Categories, func(a, b CategoryAverage) int {
		return cmp.Or(cmp.Compare(a.Monthly, b.Monthly), strings.Compare(a.Name, b.Name))
	})
	return r.VariableMonthly(), nil
}

// averageWindow renvoie les bornes des months derniers mois complets avant
// today, réduits aux mois entièrement couverts par l'historique (first :
// premier jour connu), et leur nombre.
func averageWindow(today calendar.Date, months int, first calendar.Date) (from, to calendar.Date, n int) {
	t := today.Time()
	current := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	start := current.AddDate(0, -months, 0)
	if f := first.Time(); f.After(start) {
		start = time.Date(f.Year(), f.Month(), 1, 0, 0, 0, 0, time.UTC)
		if f.Day() > 1 {
			start = start.AddDate(0, 1, 0)
		}
	}
	n = (current.Year()-start.Year())*12 + int(current.Month()-start.Month())
	if n <= 0 {
		return "", "", 0
	}
	return calendar.FromTime(start), calendar.FromTime(current.AddDate(0, 0, -1)), n
}

// accountList encode les comptes couverts en tableau JSON, lu par
// json_each : un seul paramètre, quel que soit leur nombre.
func accountList(names map[int64]string) (string, error) {
	ids := slices.Collect(maps.Keys(names))
	b, err := json.Marshal(ids)
	return string(b), err
}
