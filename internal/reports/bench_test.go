package reports

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/platform/db"
)

// BenchmarkAnnual établit le rapport d'une année sur cinq ans d'historique
// (20 000 opérations, catégorisées, dont une sur dix étiquetée).
func BenchmarkAnnual(b *testing.B) {
	d, err := db.Open(b.Context(), filepath.Join(b.TempDir(), "reports.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = d.Close() })
	ctx := b.Context()
	l := ledger.NewService(d)
	account, _ := l.CreateAccount(ctx, ledger.AccountInput{Name: "Courant", Kind: ledger.AccountCurrent, Bank: ledger.BankSG, OpeningDate: "2021-11-01"})
	cats, _ := l.Categories(ctx)
	start := calendar.Date("2021-11-01")
	err = d.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO tags (name) VALUES ('vacances')`); err != nil {
			return err
		}
		for i := range 20_000 {
			res, err := tx.ExecContext(ctx, `INSERT INTO transactions (account_id, date, label, amount, category_id) VALUES (?, ?, ?, ?, ?)`,
				account, start.AddDays(i*1825/20_000), fmt.Sprintf("CB COMMERCE %d", i%300), -int64(i%9000+100), cats[i%len(cats)].ID)
			if err != nil {
				return err
			}
			if i%10 == 0 {
				id, _ := res.LastInsertId()
				if _, err := tx.ExecContext(ctx, `INSERT INTO transaction_tags (transaction_id, tag_id) VALUES (?, 1)`, id); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		b.Fatal(err)
	}
	s := NewService(d, l)
	for b.Loop() {
		if r, err := s.Annual(ctx, 2025); err != nil || len(r.Months) != 12 {
			b.Fatal(err)
		}
	}
}
