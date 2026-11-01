package ledger

import (
	"errors"
	"testing"
)

func TestTransferCandidates(t *testing.T) {
	s := newTestService(t)
	ctx := t.Context()
	courant := mustAccount(t, s, "Courant", 0, "2026-01-01")
	livret := mustAccount(t, s, "Livret", 0, "2026-01-01")
	courses := categoryID(t, s, "Alimentation › Courses")

	out := mustTx(t, s, TransactionInput{AccountID: courant, Date: "2026-10-10", Label: "VIR EPARGNE", Amount: -200_00, CategoryID: courses})
	in := mustTx(t, s, TransactionInput{AccountID: livret, Date: "2026-10-12", Label: "VIR RECU", Amount: 200_00})
	// Non proposées : même compte, trop éloignée, montant différent.
	mustTx(t, s, TransactionInput{AccountID: courant, Date: "2026-10-10", Label: "Remboursement", Amount: 200_00})
	mustTx(t, s, TransactionInput{AccountID: livret, Date: "2026-10-20", Label: "Autre", Amount: 200_00})
	mustTx(t, s, TransactionInput{AccountID: livret, Date: "2026-10-11", Label: "Autre", Amount: 199_99})

	if n, err := s.CountTransferCandidates(ctx, 10); err != nil || n != 1 {
		t.Errorf("nombre de candidats : %d, %v", n, err)
	}
	got, err := s.TransferCandidates(ctx, 10)
	if err != nil || len(got) != 1 || got[0].Debit.ID != out || got[0].Credit.ID != in || got[0].Credit.AccountName != "Livret" {
		t.Fatalf("candidats : %+v, %v", got, err)
	}

	if err := s.LinkTransfer(ctx, out, in); err != nil {
		t.Fatal(err)
	}
	leg, _ := s.Transaction(ctx, out)
	if !leg.IsTransfer() || leg.CategoryID != 0 || leg.CounterpartAccountName != "Livret" {
		t.Errorf("jambe reliée : %+v", leg)
	}
	if got, _ = s.TransferCandidates(ctx, 10); len(got) != 0 {
		t.Errorf("une paire reliée ne doit plus être proposée : %+v", got)
	}
	err = s.LinkTransfer(ctx, out, in)
	expectInputError(t, err, "déjà partie")

	// Défaire le virement : les opérations restent, la paire n'est plus proposée.
	if err := s.UnlinkTransfer(ctx, leg.TransferID); err != nil {
		t.Fatal(err)
	}
	if leg, _ = s.Transaction(ctx, out); leg.IsTransfer() {
		t.Error("le virement doit être défait")
	}
	if got, _ = s.TransferCandidates(ctx, 10); len(got) != 0 {
		t.Errorf("une paire défaite ne doit plus être proposée : %+v", got)
	}
	if err := s.UnlinkTransfer(ctx, leg.TransferID); !errors.Is(err, ErrNotFound) {
		t.Errorf("virement inexistant : %v", err)
	}
}

func TestDismissAndLinkValidation(t *testing.T) {
	s := newTestService(t)
	ctx := t.Context()
	a := mustAccount(t, s, "A", 0, "2026-01-01")
	b := mustAccount(t, s, "B", 0, "2026-01-01")
	out := mustTx(t, s, TransactionInput{AccountID: a, Date: "2026-10-10", Label: "x", Amount: -50_00})
	in1 := mustTx(t, s, TransactionInput{AccountID: b, Date: "2026-10-10", Label: "y", Amount: 50_00})
	in2 := mustTx(t, s, TransactionInput{AccountID: b, Date: "2026-10-11", Label: "z", Amount: 50_00})
	same := mustTx(t, s, TransactionInput{AccountID: a, Date: "2026-10-10", Label: "w", Amount: 50_00})

	got, _ := s.TransferCandidates(ctx, 10)
	if len(got) != 2 || got[0].Credit.ID != in1 {
		t.Fatalf("deux crédits possibles, le plus proche d'abord : %+v", got)
	}
	if err := s.DismissTransferCandidate(ctx, out, in1); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.TransferCandidates(ctx, 10); len(got) != 1 || got[0].Credit.ID != in2 {
		t.Errorf("après refus : %+v", got)
	}
	if err := s.DismissTransferCandidate(ctx, out, 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("refus d'une opération inconnue : %v", err)
	}

	expectInputError(t, s.LinkTransfer(ctx, out, same), "deux comptes")
	expectInputError(t, s.LinkTransfer(ctx, in1, in2), "même montant")
	if err := s.LinkTransfer(ctx, out, 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("opération inconnue : %v", err)
	}
}
