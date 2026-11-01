package exports

import (
	"database/sql"
	"fmt"
	"io"
	"testing"

	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/platform/calendar"
)

// BenchmarkOperationsCSV exporte cinq ans d'historique (20 000
// opérations) : lues au fil de l'écriture, sans liste en mémoire.
func BenchmarkOperationsCSV(b *testing.B) {
	e := newEnv(b)
	ctx := b.Context()
	start := calendar.Date("2021-11-01")
	err := e.db.Tx(ctx, func(tx *sql.Tx) error {
		for i := range 20_000 {
			if _, err := tx.ExecContext(ctx, `INSERT INTO transactions (account_id, date, label, amount, note) VALUES (?, ?, ?, ?, ?)`,
				e.account, start.AddDays(i*1825/20_000), fmt.Sprintf("CB COMMERCE %d", i%300), -int64(i%9000+100), "note"); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		if err := e.s.Operations(ctx, io.Discard, ledger.Filter{}); err != nil {
			b.Fatal(err)
		}
	}
}
