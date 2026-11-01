package forecast

import (
	"testing"

	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/loans"
	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/recurring"
	"github.com/codecolors/buddyget/internal/scenarios"
)

func TestPlanChanges(t *testing.T) {
	p := plan{Rule: recurring.Rule{Amount: -100_00}}
	p.set("2027-03-01", -120_00)
	p.set("2027-01-01", -110_00)
	p.set("2027-03-01", -130_00) // à date égale, le dernier l'emporte
	p.set("2027-06-01", 0)
	p.set("2027-09-01", -150_00) // après l'arrêt : sans effet
	for nominal, want := range map[calendar.Date]int64{
		"2026-12-31": -100_00, "2027-01-01": -110_00, "2027-03-01": -130_00, "2027-05-31": -130_00, "2027-06-01": 0, "2027-10-01": 0,
	} {
		if got := p.amountAt(nominal); got != want {
			t.Errorf("%s : %d, attendu %d", nominal, got, want)
		}
	}
}

func TestForecastScenario(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	current := e.account("Courant", ledger.AccountCurrent, 3_000_00)
	salary := e.rule(recurring.RuleInput{Label: "Salaire", AccountID: current, Amount: 2_000_00, Period: calendar.Monthly, Start: "2026-11-25"})
	streaming := e.rule(recurring.RuleInput{Label: "Streaming", AccountID: current, Amount: -15_00, Period: calendar.Monthly, Start: "2026-11-10"})
	loan, err := e.loans.Create(ctx, loans.Input{Name: "Auto", AccountID: current,
		Params: loans.Params{Principal: 12_000_00, Months: 12, First: "2026-11-05"}})
	if err != nil {
		t.Fatal(err)
	}
	q := Query{Target: "2027-01-31"}
	// 3 000 + 3 × (2 000 − 15 − 1 000).
	if r := e.forecast(q); r.Closing != 5_955_00 || r.Scenario != "" {
		t.Fatalf("référence : %d", r.Closing)
	}

	id, err := e.scenarios.Create(ctx, "Grand changement")
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range []scenarios.Item{
		{Kind: scenarios.OneOff, AccountID: current, Date: "2026-12-15", Label: "Vélo", Amount: -800_00},
		{Kind: scenarios.Suspend, RuleID: streaming, Date: "2026-12-01"},
		{Kind: scenarios.Vary, RuleID: salary, Date: "2027-01-01", Percent: 1_000},
		{Kind: scenarios.Repeating, AccountID: current, Date: "2026-11-20", Label: "Piano", Amount: -60_00, Months: 1},
		{Kind: scenarios.NewLoan, AccountID: current, Date: "2027-01-05", Label: "Moto", Amount: 6_000_00, Months: 6},
		{Kind: scenarios.Prepay, LoanID: loan, Date: "2026-12-20", Amount: 50_000_00},
	} {
		if _, err := e.scenarios.AddItem(ctx, id, it); err != nil {
			t.Fatalf("%+v : %v", it, err)
		}
	}
	q.ScenarioID = id
	c, err := e.s.Compare(ctx, q, today)
	if err != nil {
		t.Fatal(err)
	}
	r := c.Scenario
	type line struct {
		date     calendar.Date
		label    string
		amount   int64
		scenario bool
	}
	var got []line
	for _, l := range r.Lines {
		got = append(got, line{l.Date, l.Label, l.Amount, l.Scenario})
	}
	// Vélo, piano, salaire de janvier + 10 %, moto ; streaming arrêté en
	// décembre ; Auto soldé le 20 décembre (10 000 € restants) : plus de
	// mensualité en janvier.
	want := []line{
		{"2026-11-05", "Crédit Auto", -1_000_00, false},
		{"2026-11-10", "Streaming", -15_00, false},
		{"2026-11-20", "Piano", -60_00, true},
		{"2026-11-25", "Salaire", 2_000_00, false},
		{"2026-12-07", "Crédit Auto", -1_000_00, false}, // samedi 5
		{"2026-12-15", "Vélo", -800_00, true},
		{"2026-12-20", "Remboursement anticipé : Auto", -10_000_00, true},
		{"2026-12-21", "Piano", -60_00, true},
		{"2026-12-28", "Salaire", 2_000_00, false}, // vendredi 25, Noël
		{"2027-01-05", "Crédit Moto", -1_000_00, true},
		{"2027-01-20", "Piano", -60_00, true},
		{"2027-01-25", "Salaire", 2_200_00, true},
	}
	if len(got) != len(want) {
		t.Fatalf("lignes :\n%v\nattendu :\n%v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ligne %d : %v, attendu %v", i, got[i], want[i])
		}
	}
	if r.Scenario != "Grand changement" || c.Reference.Closing != 5_955_00 || r.Closing != -4_795_00 || c.Gap() != -10_750_00 {
		t.Errorf("comparaison : %q, %d → %d (écart %d)", r.Scenario, c.Reference.Closing, r.Closing, c.Gap())
	}
	if c.LowGap() != r.Low.Balance-c.Reference.Low.Balance || r.Low.Balance >= 0 {
		t.Errorf("points bas : %+v / %+v", r.Low, c.Reference.Low)
	}

	// Le scénario ne touche pas aux données réelles.
	if rules, _ := e.recurring.Rules(ctx); len(rules) != 3 {
		t.Errorf("récurrences : %d", len(rules))
	}
	if _, err := e.s.Forecast(ctx, Query{Target: "2027-01-31", ScenarioID: 999}, today); err == nil {
		t.Error("scénario inconnu accepté")
	}
}
