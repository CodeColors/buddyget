package loans

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/platform/db"
	"github.com/codecolors/buddyget/internal/platform/validation"
	"github.com/codecolors/buddyget/internal/recurring"
)

func TestLoanService(t *testing.T) {
	d, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "loans.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	ctx := t.Context()
	l := ledger.NewService(d)
	account, _ := l.CreateAccount(ctx, ledger.AccountInput{Name: "Courant", Kind: ledger.AccountCurrent, Bank: ledger.BankSG, OpeningDate: "2026-01-01"})
	r := recurring.NewService(d)
	s := NewService(d, r)

	in := Input{Name: "Voiture", AccountID: account, Params: Params{Principal: 12_000_00, Rate: 4_900, Months: 48, Insurance: 8_00, First: "2026-12-05"}}
	for _, bad := range []func(*Input){
		func(i *Input) { i.Name = " " },
		func(i *Input) { i.Rate = 100_001 },
		func(i *Input) { i.Months = 0 },
		func(i *Input) { i.AccountID = 999 },
	} {
		b := in
		bad(&b)
		if _, err := s.Create(ctx, b); !isInput(err) {
			t.Errorf("saisie invalide acceptée : %+v (%v)", b, err)
		}
	}
	id, err := s.Create(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, in); !isInput(err) {
		t.Error("nom en double accepté")
	}
	loan, _ := s.Loan(ctx, id)
	sched := loan.Schedule()
	rule, err := r.Rule(ctx, loan.RuleID)
	if err != nil || rule.Label != "Crédit Voiture" || rule.Amount != -(sched.Payment+8_00) || rule.End != sched.End || rule.End != "2030-11-05" {
		t.Fatalf("mensualité générée : %+v, %v (fin %s)", rule, err, sched.End)
	}

	// Remboursement anticipé : la mensualité s'arrête plus tôt.
	if err := s.Prepay(ctx, id, Prepayment{Date: "2027-06-01", Amount: 4_000_00}); err != nil {
		t.Fatal(err)
	}
	if rule, _ = r.Rule(ctx, loan.RuleID); rule.End >= "2030-11-05" {
		t.Errorf("fin après remboursement anticipé : %s", rule.End)
	}
	if err := s.Prepay(ctx, 999, Prepayment{Date: "2027-06-01", Amount: 1}); !errors.Is(err, ErrNotFound) {
		t.Errorf("crédit inconnu : %v", err)
	}

	// Modification ; la récurrence supprimée à la main est recréée.
	in.Months = 36
	if err := r.DeleteRule(ctx, loan.RuleID); err != nil {
		t.Fatal(err)
	}
	if err := s.Update(ctx, id, in); err != nil {
		t.Fatal(err)
	}
	loans, _ := s.Loans(ctx)
	if len(loans) != 1 || loans[0].Months != 36 || len(loans[0].Prepayments) != 1 {
		t.Errorf("après modification : %+v", loans)
	}
	if rule, err := r.Rule(ctx, loans[0].RuleID); err != nil || rule.Label != "Crédit Voiture" {
		t.Errorf("mensualité recréée : %+v, %v", rule, err)
	}
	if err := s.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	if rules, _ := r.Rules(ctx); len(rules) != 0 {
		t.Errorf("la mensualité disparaît avec le crédit : %+v", rules)
	}
	if err := s.Update(ctx, id, in); !errors.Is(err, ErrNotFound) {
		t.Errorf("crédit supprimé : %v", err)
	}
}

func TestLoanSettledEarly(t *testing.T) {
	d, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "loans.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	ctx := t.Context()
	account, _ := ledger.NewService(d).CreateAccount(ctx, ledger.AccountInput{Name: "Courant", Kind: ledger.AccountCurrent, Bank: ledger.BankSG, OpeningDate: "2026-01-01"})
	r := recurring.NewService(d)
	s := NewService(d, r)
	in := Input{Name: "Travaux", AccountID: account, Params: Params{Principal: 3_000_00, Months: 3, First: "2026-12-05"}}

	// Soldé après la première échéance : la mensualité s'arrête le jour du
	// remboursement.
	id, err := s.Create(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Prepay(ctx, id, Prepayment{Date: "2026-12-20", Amount: 5_000_00}); err != nil {
		t.Fatal(err)
	}
	loan, _ := s.Loan(ctx, id)
	if rule, err := r.Rule(ctx, loan.RuleID); err != nil || rule.End != "2026-12-20" {
		t.Errorf("mensualité après solde : %+v, %v", rule, err)
	}

	// Soldé avant la première échéance : plus de mensualité.
	in.Name = "Cuisine"
	id, err = s.Create(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Prepay(ctx, id, Prepayment{Date: "2026-11-20", Amount: 3_000_00}); err != nil {
		t.Fatal(err)
	}
	if loan, _ := s.Loan(ctx, id); loan.RuleID != 0 {
		t.Errorf("mensualité d'un crédit soldé d'avance : %d", loan.RuleID)
	}
}

func isInput(err error) bool {
	_, ok := validation.Message(err)
	return ok
}
