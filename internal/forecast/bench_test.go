package forecast

import (
	"database/sql"
	"fmt"
	"testing"

	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/recurring"
)

// BenchmarkForecast calcule un prévisionnel à 24 mois sur deux comptes
// courants riches de cinq ans d'historique (20 000 opérations, aucune
// pointée) et de 30 récurrences, avec la moyenne sur 12 mois et le solde
// pointé : le cas le plus coûteux.
func BenchmarkForecast(b *testing.B) {
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
	for b.Loop() {
		r, err := e.s.Forecast(ctx, Query{Target: MaxTarget(today)}, today)
		if err != nil || len(r.Days) < 730 || r.AverageMonths != 12 {
			b.Fatalf("%d jours, %d mois moyennés, %v", len(r.Days), r.AverageMonths, err)
		}
	}
}
