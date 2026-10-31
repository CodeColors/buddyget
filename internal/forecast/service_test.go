package forecast

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/platform/db"
	"github.com/codecolors/buddyget/internal/platform/validation"
	"github.com/codecolors/buddyget/internal/recurring"
)

const today calendar.Date = "2026-10-31"

type env struct {
	t         testing.TB
	db        *db.DB
	s         *Service
	ledger    *ledger.Service
	recurring *recurring.Service
}

func newEnv(t testing.TB) *env {
	t.Helper()
	d, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "forecast.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	l, r := ledger.NewService(d), recurring.NewService(d)
	return &env{t: t, db: d, s: NewService(d, l, r), ledger: l, recurring: r}
}

func (e *env) account(name string, kind ledger.AccountKind, opening int64) int64 {
	e.t.Helper()
	id, err := e.ledger.CreateAccount(e.t.Context(), ledger.AccountInput{
		Name: name, Kind: kind, Bank: ledger.BankSG, OpeningBalance: opening, OpeningDate: "2026-07-01",
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *env) tx(in ledger.TransactionInput) {
	e.t.Helper()
	if _, err := e.ledger.CreateTransaction(e.t.Context(), in); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) rule(in recurring.RuleInput) int64 {
	e.t.Helper()
	if in.Shift == "" {
		in.Shift = calendar.NextBusinessDay
	}
	in.Origin = recurring.Manual
	id, err := e.recurring.CreateRule(e.t.Context(), in)
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *env) category(name string) int64 {
	e.t.Helper()
	cats, err := e.ledger.Categories(e.t.Context())
	if err != nil {
		e.t.Fatal(err)
	}
	for _, c := range cats {
		if c.FullName() == name {
			return c.ID
		}
	}
	e.t.Fatalf("catégorie %q introuvable", name)
	return 0
}

func (e *env) forecast(q Query) Report {
	e.t.Helper()
	r, err := e.s.Forecast(e.t.Context(), q, today)
	if err != nil {
		e.t.Fatal(err)
	}
	return r
}

// household crée un foyer type : deux comptes courants, un livret, des
// récurrences, des opérations planifiées et quatre mois d'historique.
func household(t *testing.T) (*env, map[string]int64) {
	e := newEnv(t)
	ids := map[string]int64{
		"courant": e.account("Courant", ledger.AccountCurrent, 1000_00),
		"joint":   e.account("Joint", ledger.AccountCurrent, 200_00),
		"livret":  e.account("Livret", ledger.AccountSavings, 5000_00),
	}
	archived := e.account("Ancien", ledger.AccountCurrent, 300_00)
	if err := e.ledger.SetAccountArchived(t.Context(), archived, true); err != nil {
		t.Fatal(err)
	}
	courses, restaurants := e.category("Alimentation › Courses"), e.category("Alimentation › Restaurants")
	for _, month := range []string{"07", "08", "09", "10"} {
		e.tx(ledger.TransactionInput{AccountID: ids["courant"], Date: calendar.Date("2026-" + month + "-05"), Label: "CARREFOUR", Amount: -300_00, CategoryID: courses})
		e.tx(ledger.TransactionInput{AccountID: ids["courant"], Date: calendar.Date("2026-" + month + "-15"), Label: "PRLV EDF " + month, Amount: -80_00})
		e.tx(ledger.TransactionInput{AccountID: ids["courant"], Date: calendar.Date("2026-" + month + "-28"), Label: "SALAIRE", Amount: 2000_00, Cleared: true})
	}
	e.tx(ledger.TransactionInput{AccountID: ids["courant"], Date: "2026-08-20", Label: "Restaurant", Amount: -60_00, CategoryID: restaurants})

	e.rule(recurring.RuleInput{Label: "Loyer", AccountID: ids["courant"], Amount: -700_00, Period: calendar.Monthly, Start: "2026-01-01"})
	e.rule(recurring.RuleInput{Label: "PRLV EDF", AccountID: ids["courant"], Amount: -80_00, Estimated: true, Period: calendar.Monthly, Start: "2026-07-15"})
	e.rule(recurring.RuleInput{Label: "Salaire", AccountID: ids["courant"], Amount: 2000_00, Period: calendar.Monthly, Shift: calendar.PreviousBusinessDay, Start: "2026-07-28"})
	e.rule(recurring.RuleInput{Label: "Assurance auto", AccountID: ids["courant"], Amount: -400_00, Period: calendar.Yearly, Start: "2025-12-20", End: "2026-12-31"})
	e.rule(recurring.RuleInput{Label: "Intérêts", AccountID: ids["livret"], Amount: 10_00, Period: calendar.Monthly, Start: "2026-01-31", LastDay: true})
	netflix := e.rule(recurring.RuleInput{Label: "Netflix", AccountID: ids["courant"], Amount: -18_00, Period: calendar.Monthly, Start: "2026-01-10"})
	if err := e.recurring.SetActive(t.Context(), netflix, false); err != nil {
		t.Fatal(err)
	}

	e.tx(ledger.TransactionInput{AccountID: ids["courant"], Date: "2026-11-15", Label: "Garage", Amount: -120_00})
	for to, date := range map[string]calendar.Date{"joint": "2026-11-20", "livret": "2026-11-25"} {
		if _, err := e.ledger.CreateTransfer(t.Context(), ledger.TransferInput{
			FromAccountID: ids["courant"], ToAccountID: ids[to], Date: date, Label: "Virement " + to, Amount: 100_00,
		}); err != nil {
			t.Fatal(err)
		}
	}
	return e, ids
}

func TestForecastGathersEverything(t *testing.T) {
	e, _ := household(t)
	rules, _ := e.recurring.Rules(t.Context())
	loyer := rules[slices.IndexFunc(rules, func(r recurring.Rule) bool { return r.Label == "Loyer" })].ID
	if err := e.s.SetExcluded(t.Context(), loyer, "2026-12-01", true, today); err != nil {
		t.Fatal(err)
	}

	r := e.forecast(Query{Target: "2026-12-31"})
	if len(r.Accounts) != 2 || r.Accounts[0].Name != "Courant" || r.Accounts[1].Name != "Joint" {
		t.Fatalf("comptes courants actifs seulement : %+v", r.Accounts)
	}
	// Courant : 1 000 + 4 × (2 000 − 300 − 80) − 60 ; Joint : 200.
	if r.Opening != 7420_00+200_00 {
		t.Errorf("solde actuel : %d", r.Opening)
	}
	type line struct {
		date   calendar.Date
		label  string
		amount int64
		off    bool
	}
	var got []line
	for _, l := range r.Lines {
		got = append(got, line{l.Date, l.Label, l.Amount, l.Excluded})
	}
	want := []line{
		{"2026-11-02", "Loyer", -700_00, false}, // dimanche 1er novembre, Toussaint
		{"2026-11-15", "Garage", -120_00, false},
		{"2026-11-16", "PRLV EDF", -80_00, false}, // dimanche 15
		{"2026-11-25", "Virement livret", -100_00, false},
		{"2026-11-27", "Salaire", 2000_00, false}, // samedi 28 : la veille
		{"2026-12-01", "Loyer", -700_00, true},
		{"2026-12-15", "PRLV EDF", -80_00, false},
		{"2026-12-21", "Assurance auto", -400_00, false}, // dimanche 20
		{"2026-12-28", "Salaire", 2000_00, false},
	}
	if !slices.Equal(got, want) {
		t.Errorf("lignes :\n%v\nattendu :\n%v", got, want)
	}
	// Moyenne de juillet à septembre : courses 300 €, restaurant 60/3 € ;
	// EDF relève d'une récurrence, octobre n'est pas terminé.
	wantCats := []CategoryAverage{{"Alimentation › Courses", "#43a047", -300_00}, {"Alimentation › Restaurants", "#43a047", -20_00}}
	if r.AverageMonths != 3 || !slices.Equal(r.Categories, wantCats) {
		t.Errorf("dépenses variables sur %d mois : %+v", r.AverageMonths, r.Categories)
	}
	wantTotals := Totals{Income: 4000_00, Recurring: -1260_00, Other: -220_00, Variable: -640_00}
	if r.Totals != wantTotals || r.Closing != 9500_00 {
		t.Errorf("totaux : %+v, solde prévu %d", r.Totals, r.Closing)
	}

	// L'échéance réintégrée compte de nouveau.
	if err := e.s.SetExcluded(t.Context(), loyer, "2026-12-01", false, today); err != nil {
		t.Fatal(err)
	}
	if r = e.forecast(Query{Target: "2026-12-31"}); r.Closing != 8800_00 {
		t.Errorf("après réintégration : %d", r.Closing)
	}
	if err := e.s.SetExcluded(t.Context(), 999, "2026-12-01", true, today); !errors.Is(err, recurring.ErrNotFound) {
		t.Errorf("récurrence inconnue : %v", err)
	}
}

func TestForecastSettings(t *testing.T) {
	e, _ := household(t)
	ctx := t.Context()
	if st, err := e.s.Settings(ctx); err != nil || st != (Settings{AverageMonths: 3}) {
		t.Fatalf("réglages par défaut : %+v, %v", st, err)
	}
	if err := e.s.SaveSettings(ctx, Settings{AverageMonths: 5}); !isInputError(err) {
		t.Errorf("profondeur invalide : %v", err)
	}
	if err := e.s.SaveSettings(ctx, Settings{Threshold: 9000_00, ClearedOnly: true}); err != nil {
		t.Fatal(err)
	}
	r := e.forecast(Query{Target: "2026-12-31"})
	if r.AverageMonths != 0 || len(r.Categories) != 0 || r.Totals.Variable != 0 {
		t.Errorf("sans estimation des dépenses variables : %+v", r.Totals)
	}
	// Solde pointé : les salaires seulement ; le reste arrive le lendemain.
	if r.Opening != 1000_00+8000_00+200_00 || r.Lines[0].Kind != Pending || r.Lines[0].Date != "2026-11-01" ||
		r.Lines[0].Amount != -1580_00 || r.Lines[0].Label != "9 opérations non pointées" {
		t.Errorf("solde pointé : %d, première ligne %+v", r.Opening, r.Lines[0])
	}
	if r.Closing != 8800_00+640_00 { // loyer de décembre compris, sans dépenses variables
		t.Errorf("le solde prévu ne dépend pas du pointage : %d", r.Closing)
	}
	if r.Threshold != 9000_00 || len(r.Below) == 0 || r.Below[0].From != "2026-11-01" {
		t.Errorf("seuil : %+v", r.Below)
	}
}

func TestForecastAccountAndTarget(t *testing.T) {
	e, ids := household(t)
	r := e.forecast(Query{AccountID: ids["livret"], Target: "2027-01-31"})
	// Les intérêts du samedi 31 octobre arrivent le lundi 2 novembre ; ceux
	// du dimanche 31 janvier 2027, le 1er février, après la date cible.
	if len(r.Accounts) != 1 || r.Opening != 5000_00 || len(r.Lines) != 4 || r.Closing != 5130_00 ||
		r.Lines[0].Date != "2026-11-02" || r.Lines[0].Nominal != today || r.Lines[3].Date != "2026-12-31" {
		t.Errorf("livret : solde %d → %d, lignes %+v", r.Opening, r.Closing, r.Lines)
	}
	ctx := t.Context()
	if _, err := e.s.Forecast(ctx, Query{AccountID: 999, Target: "2026-12-31"}, today); !errors.Is(err, ledger.ErrNotFound) {
		t.Errorf("compte inconnu : %v", err)
	}
	for _, target := range []calendar.Date{today, "2026-01-01", "2028-11-01"} {
		if _, err := e.s.Forecast(ctx, Query{Target: target}, today); !isInputError(err) {
			t.Errorf("date cible %s : %v", target, err)
		}
	}
	if _, err := newEnv(t).s.Forecast(ctx, Query{Target: "2026-12-31"}, today); !isInputError(err) {
		t.Errorf("sans compte courant : %v", err)
	}
}

func TestForecastLongHorizon(t *testing.T) {
	e := newEnv(t)
	id := e.account("Courant", ledger.AccountCurrent, 0)
	e.rule(recurring.RuleInput{Label: "Fin de mois", AccountID: id, Amount: -1_00, Period: calendar.Monthly, LastDay: true, Shift: calendar.NoShift, Start: "2026-01-31"})
	e.rule(recurring.RuleInput{Label: "Crédit", AccountID: id, Amount: -10_00, Period: calendar.Monthly, Start: "2026-01-29", End: "2027-06-30"})
	r := e.forecast(Query{Target: MaxTarget(today)})
	var ends, credit []calendar.Date
	for _, l := range r.Lines {
		if l.Label == "Fin de mois" {
			ends = append(ends, l.Date)
		} else {
			credit = append(credit, l.Nominal)
		}
	}
	if len(ends) != 24 || !slices.Contains(ends, "2028-02-29") || !slices.Contains(ends, "2027-02-28") || ends[23] != "2028-10-31" {
		t.Errorf("échéances de fin de mois : %v", ends)
	}
	// Une récurrence du 29 tombe le 28 février 2027 et s'arrête après
	// l'échéance de juin 2027.
	if len(credit) != 8 || credit[3] != "2027-02-28" || credit[7] != "2027-06-29" {
		t.Errorf("crédit terminé : %v", credit)
	}
}

func TestAverageWindow(t *testing.T) {
	for _, c := range []struct {
		first    calendar.Date
		from, to calendar.Date
		n        int
	}{
		{"2020-01-01", "2026-07-01", "2026-09-30", 3},
		{"2026-08-01", "2026-08-01", "2026-09-30", 2},
		{"2026-08-15", "2026-09-01", "2026-09-30", 1},
		{"2026-10-02", "", "", 0},
	} {
		if from, to, n := averageWindow(today, 3, c.first); from != c.from || to != c.to || n != c.n {
			t.Errorf("historique depuis %q : %s → %s (%d mois)", c.first, from, to, n)
		}
	}
}

func isInputError(err error) bool {
	_, ok := validation.Message(err)
	return ok
}
