package backup

import (
	"database/sql"
	"fmt"
	"testing"
	"time"
)

// BenchmarkBackup sauvegarde, chiffrée, une base de cinq ans d'historique
// (20 000 opérations) : instantané, archive compressée, chiffrement.
func BenchmarkBackup(b *testing.B) {
	e := newEnv(b, passphrase)
	ctx := b.Context()
	err := e.db.Tx(ctx, func(tx *sql.Tx) error {
		for i := range 20_000 {
			if _, err := tx.ExecContext(ctx, `INSERT INTO transactions (account_id, date, label, amount) VALUES (?, '2026-01-01', ?, ?)`,
				e.account, fmt.Sprintf("CB COMMERCE %d", i%300), -int64(i%9000+100)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		b.Fatal(err)
	}
	st, err := e.s.Settings(ctx)
	if err != nil {
		b.Fatal(err)
	}
	at := now
	for b.Loop() {
		at = at.Add(time.Second)
		a, err := e.s.Run(ctx, st, false, at)
		if err != nil {
			b.Fatal(err)
		}
		b.ReportMetric(float64(a.Size)/1024, "Kio/archive")
	}
}
