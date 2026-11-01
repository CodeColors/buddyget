package ledger

import (
	"errors"
	"testing"

	"github.com/codecolors/buddyget/internal/platform/calendar"
)

func TestReconciliation(t *testing.T) {
	s := newTestService(t)
	ctx := t.Context()
	id := mustAccount(t, s, "Courant", 1000_00, "2026-10-01")
	mustTx(t, s, TransactionInput{AccountID: id, Date: "2026-10-05", Label: "Courses", Amount: -80_00, Cleared: true})
	mustTx(t, s, TransactionInput{AccountID: id, Date: "2026-10-20", Label: "Essence", Amount: -60_00})
	mustTx(t, s, TransactionInput{AccountID: id, Date: "2026-11-02", Label: "Après le relevé", Amount: -5_00})

	r, err := s.Reconciliation(ctx, id)
	if err != nil || r.HasBankBalance || r.Account.Name != "Courant" {
		t.Fatalf("sans solde bancaire : %+v, %v", r, err)
	}

	if err := s.SetBankBalance(ctx, id, 900_00, "2026-10-31"); err != nil {
		t.Fatal(err)
	}
	r, _ = s.Reconciliation(ctx, id)
	if !r.HasBankBalance || r.Computed != 860_00 || r.Gap() != 40_00 || r.Uncleared != 1 || r.BankDate != "2026-10-31" {
		t.Errorf("rapprochement : %+v (écart %d)", r, r.Gap())
	}

	expectInputError(t, s.SetBankBalance(ctx, id, 1, ""), "date")
	if err := s.SetBankBalance(ctx, 999, 1, "2026-10-31"); !errors.Is(err, ErrNotFound) {
		t.Errorf("compte inconnu : %v", err)
	}
	if _, err := s.Reconciliation(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("compte inconnu : %v", err)
	}

	for on, want := range map[string]Uncleared{"2026-10-31": {1, -60_00}, "2026-11-30": {2, -65_00}} {
		if u, err := s.UnclearedByAccount(ctx, calendar.Date(on)); err != nil || u[id] != want {
			t.Errorf("non pointées au %s : %+v, %v", on, u, err)
		}
	}
}
