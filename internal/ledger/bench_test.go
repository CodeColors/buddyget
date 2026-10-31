package ledger

import (
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/codecolors/buddyget/internal/platform/calendar"
)

// benchOperations correspond à cinq ans d'opérations d'un foyer actif.
const benchOperations = 20_000

func benchService(b *testing.B) *Service {
	b.Helper()
	s := newTestService(b)
	ctx := b.Context()
	courant := mustAccount(b, s, "Courant", 0, "2021-01-01")
	labels := []string{"CARTE Café de la Gare", "Supermarché Lidl", "Boulangerie Martin", "Prélèvement EDF", "Virement salaire", "Pharmacie du centre"}
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		start := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
		for i := range benchOperations {
			_, err := tx.ExecContext(ctx,
				`INSERT INTO transactions (account_id, date, label, amount, note) VALUES (?, ?, ?, ?, ?)`,
				courant, calendar.FromTime(start.AddDate(0, 0, i*5/55)), fmt.Sprintf("%s %d", labels[i%len(labels)], i), -int64(i%9000+100), "")
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		b.Fatal(err)
	}
	return s
}

func BenchmarkSearch(b *testing.B) {
	s := benchService(b)
	for name, f := range map[string]Filter{
		"sans critère":       {},
		"plein texte":        {Query: "cafe gare"},
		"période et montant": {From: "2024-01-01", To: "2024-12-31", MinAmount: 50_00},
	} {
		b.Run(name, func(b *testing.B) {
			for b.Loop() {
				if _, err := s.Search(b.Context(), f, 0); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkAccounts(b *testing.B) {
	s := benchService(b)
	for b.Loop() {
		if _, err := s.Accounts(b.Context(), "2026-10-31"); err != nil {
			b.Fatal(err)
		}
	}
}
