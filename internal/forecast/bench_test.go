package forecast

import (
	"database/sql"
	"fmt"
	"testing"

	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/loans"
	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/recurring"
	"github.com/codecolors/buddyget/internal/scenarios"
)

// heavyHousehold prépare deux comptes courants riches de cinq ans
// d'historique (20 000 opérations, aucune pointée) et de 30 récurrences,
// avec la moyenne sur 12 mois et le solde pointé : le cas le plus coûteux.
func heavyHousehold(b *testing.B) (*env, []int64) {
	b.Helper()
	e := newEnv(b)
	ctx := b.Context()
	accounts := []int64{e.account("Courant", ledger.AccountCurrent, 0), e.account("Joint", ledger.AccountCurrent, 0)}
	cats, err := e.ledger.Categories(ctx)
	if err != nil {
		b.Fatal(err)
	}
	start := calendar.Date("2021-11-01")
	if _, err := e.db.W.ExecContext(ctx, `UPDATE accounts SET opening_date = ?`, start); err != nil {
		b.Fatal(err)
	}
	err = e.db.Tx(ctx, func(tx *sql.Tx) error {
		for i := range 20_000 {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO transactions (account_id, date, label, amount, category_id) VALUES (?, ?, ?, ?, ?)`,
				accounts[i%2], start.AddDays(i*1825/20_000), fmt.Sprintf("CB COMMERCE %d", i%300), -int64(i%9000+100), cats[i%len(cats)].ID,
			); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		b.Fatal(err)
	}
	periods := []calendar.Period{calendar.Weekly, calendar.Monthly, calendar.Quarterly}
	for i := range 30 {
		e.rule(recurring.RuleInput{
			Label: fmt.Sprintf("Échéance %d", i), AccountID: accounts[i%2], Amount: -int64(1000 + i*100),
			Period: periods[i%len(periods)], Start: start.AddDays(i),
		})
	}
	if err := e.s.SaveSettings(ctx, Settings{ClearedOnly: true, AverageMonths: 12}); err != nil {
		b.Fatal(err)
	}
	return e, accounts
}

// BenchmarkForecast calcule un prévisionnel à 24 mois du foyer chargé.
func BenchmarkForecast(b *testing.B) {
	e, _ := heavyHousehold(b)
	ctx := b.Context()
	for b.Loop() {
		r, err := e.s.Forecast(ctx, Query{Target: MaxTarget(today)}, today)
		if err != nil || len(r.Days) < 730 || r.AverageMonths != 12 {
			b.Fatalf("%d jours, %d mois moyennés, %v", len(r.Days), r.AverageMonths, err)
		}
	}
}

// BenchmarkCompareScenario compare à 24 mois, sur le foyer chargé enrichi
// de trois dépenses annuelles lissées et d'un crédit, un scénario de six
// modifications avec la référence : deux prévisionnels complets.
func BenchmarkCompareScenario(b *testing.B) {
	e, accounts := heavyHousehold(b)
	ctx := b.Context()
	for i := range 3 {
		e.rule(recurring.RuleInput{Label: fmt.Sprintf("Annuelle %d", i), AccountID: accounts[0], Amount: -int64(50_000 + i*10_000),
			Period: calendar.Yearly, Start: today.AddDays(30 * (i + 1)), Smooth: true})
	}
	loan, err := e.loans.Create(ctx, loans.Input{Name: "Auto", AccountID: accounts[0],
		Params: loans.Params{Principal: 15_000_00, Rate: 4_500, Months: 60, First: today.AddDays(5)}})
	if err != nil {
		b.Fatal(err)
	}
	rules, err := e.recurring.Rules(ctx)
	if err != nil {
		b.Fatal(err)
	}
	id, err := e.scenarios.Create(ctx, "Banc")
	if err != nil {
		b.Fatal(err)
	}
	for _, it := range []scenarios.Item{
		{Kind: scenarios.OneOff, AccountID: accounts[0], Date: today.AddDays(20), Label: "Achat", Amount: -2_000_00},
		{Kind: scenarios.Repeating, AccountID: accounts[1], Date: today.AddDays(3), Label: "Cours", Amount: -60_00, Months: 1},
		{Kind: scenarios.Suspend, RuleID: rules[0].ID, Date: today.AddDays(60)},
		{Kind: scenarios.Vary, RuleID: rules[1].ID, Date: today.AddDays(90), Percent: 1_500},
		{Kind: scenarios.NewLoan, AccountID: accounts[1], Date: today.AddDays(40), Label: "Moto", Amount: 8_000_00, Rate: 3_000, Months: 36},
		{Kind: scenarios.Prepay, LoanID: loan, Date: today.AddDays(200), Amount: 5_000_00},
	} {
		if _, err := e.scenarios.AddItem(ctx, id, it); err != nil {
			b.Fatal(err)
		}
	}
	for b.Loop() {
		c, err := e.s.Compare(ctx, Query{Target: MaxTarget(today), ScenarioID: id}, today)
		if err != nil || c.Gap() == 0 {
			b.Fatalf("écart %d, %v", c.Gap(), err)
		}
	}
}
