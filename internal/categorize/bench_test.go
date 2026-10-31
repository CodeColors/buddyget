package categorize

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/platform/db"
)

// BenchmarkApply applique 50 règles à 20 000 opérations (cinq ans d'un
// foyer) : parcours complet et correspondance de chaque libellé.
func BenchmarkApply(b *testing.B) {
	ctx := b.Context()
	d, err := db.Open(ctx, filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = d.Close() })
	l := ledger.NewService(d)
	s := NewService(d)
	account, err := l.CreateAccount(ctx, ledger.AccountInput{Name: "Courant", Kind: ledger.AccountCurrent, Bank: ledger.BankSG, OpeningDate: "2021-01-01"})
	if err != nil {
		b.Fatal(err)
	}
	cats, err := l.Categories(ctx)
	if err != nil {
		b.Fatal(err)
	}
	for i := range 50 {
		kind := MatchContains
		pattern := fmt.Sprintf("commerce %d ", i)
		if i%10 == 0 {
			kind, pattern = MatchRegex, fmt.Sprintf(`^CB COMMERCE %d\b`, i)
		}
		if _, err := s.CreateRule(ctx, RuleInput{Pattern: pattern, Match: kind, CategoryID: cats[i%len(cats)].ID}); err != nil {
			b.Fatal(err)
		}
	}
	err = d.Tx(ctx, func(tx *sql.Tx) error {
		for i := range 20_000 {
			if _, err := tx.ExecContext(ctx, `INSERT INTO transactions (account_id, date, label, amount) VALUES (?, '2026-01-01', ?, -100)`,
				account, fmt.Sprintf("CB COMMERCE %d PARIS RÉF %d", i%80, i)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		if _, err := s.Apply(ctx, 0, true); err != nil {
			b.Fatal(err)
		}
	}
}
