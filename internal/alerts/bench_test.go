package alerts

import (
	"database/sql"
	"fmt"
	"testing"

	"github.com/codecolors/buddyget/internal/budget"
	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/recurring"
)

// BenchmarkEvaluate examine les sept types d'alerte sur un foyer riche de
// cinq ans d'historique (20 000 opérations), 30 récurrences et 10
// enveloppes : c'est le travail fait après chaque saisie ou import.
func BenchmarkEvaluate(b *testing.B) {
	e := newEnv(b)
	ctx := b.Context()
	account, err := e.ledger.CreateAccount(ctx, ledger.AccountInput{Name: "Courant", Kind: ledger.AccountCurrent, Bank: ledger.BankSG, OpeningDate: "2021-11-01"})
	if err != nil {
		b.Fatal(err)
	}
	cats, _ := e.ledger.Categories(ctx)
	var expense []ledger.Category
	for _, c := range cats {
		if c.Kind == ledger.CategoryExpense {
			expense = append(expense, c)
		}
	}
	start := calendar.Date("2021-11-01")
	err = e.d.Tx(ctx, func(tx *sql.Tx) error {
		for i := range 20_000 {
			if _, err := tx.ExecContext(ctx, `INSERT INTO transactions (account_id, date, label, amount, category_id) VALUES (?, ?, ?, ?, ?)`,
				account, start.AddDays(i*1825/20_000), fmt.Sprintf("CB COMMERCE %d", i%300), -int64(i%9000+100), expense[i%len(expense)].ID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		b.Fatal(err)
	}
	for i := range 30 {
		if _, err := e.recurring.CreateRule(ctx, recurring.RuleInput{Label: fmt.Sprintf("CB COMMERCE %d", i), AccountID: account, Amount: -int64(1000 + i*100),
			Period: calendar.Monthly, Shift: calendar.NextBusinessDay, Start: start.AddDays(i), Origin: recurring.Manual}); err != nil {
			b.Fatal(err)
		}
	}
	for _, c := range expense[:10] {
		if err := e.budgets.Save(ctx, budget.Input{CategoryID: c.ID, Amount: 300_00, CarryOver: true}, "2026-01-15"); err != nil {
			b.Fatal(err)
		}
	}
	for b.Loop() {
		if _, err := e.s.Evaluate(ctx, today); err != nil {
			b.Fatal(err)
		}
	}
}
