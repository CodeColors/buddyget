package goals

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/platform/db"
	"github.com/codecolors/buddyget/internal/platform/validation"
	"github.com/codecolors/buddyget/internal/recurring"
)

func TestProgress(t *testing.T) {
	const today = calendar.Date("2026-11-01")
	g := Goal{Target: 1_200_00, Saved: 300_00, TargetDate: "2027-07-01"}
	if g.Remaining() != 900_00 || g.Percent() != 25 || g.MonthsLeft(today) != 8 {
		t.Errorf("progression : reste %d, %d %%, %d mois", g.Remaining(), g.Percent(), g.MonthsLeft(today))
	}
	if g.Suggested(today) != 112_50 {
		t.Errorf("versement suggéré : %d", g.Suggested(today))
	}
	if _, ok := g.Projected(today); ok || !g.Late(today) {
		t.Error("sans versement programmé, pas de date d'atteinte")
	}
	g.Monthly = 100_00
	if d, _ := g.Projected(today); d != "2027-08-01" || !g.Late(today) {
		t.Errorf("100 € par mois : atteint le %s", d)
	}
	g.Monthly = 150_00
	if d, _ := g.Projected(today); d != "2027-05-01" || g.Late(today) {
		t.Errorf("150 € par mois : atteint le %s", d)
	}
	// Date cible dépassée ou proche : au moins un mois ; cible atteinte.
	if g.MonthsLeft("2027-06-15") != 1 || g.MonthsLeft("2028-01-01") != 1 {
		t.Error("mois restants bornés à 1")
	}
	g.Saved = 1_500_00
	if d, ok := g.Projected(today); !ok || d != today || g.Percent() != 100 || g.Suggested(today) != 0 {
		t.Errorf("cible atteinte : %s, %d %%", d, g.Percent())
	}
}

func TestGoalService(t *testing.T) {
	d, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "goals.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	ctx := t.Context()
	const today = calendar.Date("2026-11-01")
	l := ledger.NewService(d)
	current, _ := l.CreateAccount(ctx, ledger.AccountInput{Name: "Courant", Kind: ledger.AccountCurrent, Bank: ledger.BankSG, OpeningDate: "2026-01-01"})
	livret, _ := l.CreateAccount(ctx, ledger.AccountInput{Name: "Livret A", Kind: ledger.AccountSavings, Bank: ledger.BankSG, OpeningBalance: 2_000_00, OpeningDate: "2026-01-01"})
	r := recurring.NewService(d)
	s := NewService(d, l, r)

	in := Input{Name: "Vacances", Target: 1_200_00, TargetDate: "2027-07-01", AccountID: livret, Tag: " vacances "}
	for _, bad := range []func(*Input){
		func(i *Input) { i.Name = "" },
		func(i *Input) { i.Target = 0 },
		func(i *Input) { i.TargetDate = "" },
		func(i *Input) { i.AccountID = current },
		func(i *Input) { i.AccountID = 999 },
		func(i *Input) { i.Tag = "plage, soleil" },
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
	other, err := s.Create(ctx, Input{Name: "Travaux", Target: 5_000_00, TargetDate: "2028-01-01", AccountID: livret})
	if err != nil {
		t.Fatal(err)
	}

	// Cagnottes : bornées par le solde du compte.
	if err := s.Allocate(ctx, id, 300_00, today); err != nil {
		t.Fatal(err)
	}
	if err := s.Allocate(ctx, other, 1_500_00, today); err != nil {
		t.Fatal(err)
	}
	if free, _ := s.Unallocated(ctx, livret, today); free != 200_00 {
		t.Errorf("non affecté : %d", free)
	}
	if err := s.Allocate(ctx, id, 200_01, today); !isInput(err) {
		t.Error("cagnotte au-delà du solde acceptée")
	}
	if err := s.Allocate(ctx, id, -300_01, today); !isInput(err) {
		t.Error("retrait au-delà de la cagnotte accepté")
	}
	if err := s.Allocate(ctx, other, -500_00, today); err != nil {
		t.Fatal(err)
	}

	// Versement programmé : le suggéré, du mois prochain à la date cible.
	if err := s.Schedule(ctx, id, current, today); err != nil {
		t.Fatal(err)
	}
	g, err := s.Goal(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	rule, err := r.Rule(ctx, g.RuleID)
	if err != nil || rule.Label != "Projet Vacances" || rule.Amount != -112_50 || rule.ToAccountID != livret ||
		rule.Start != "2026-12-01" || rule.End != "2027-07-01" {
		t.Fatalf("versement programmé : %+v, %v", rule, err)
	}
	if g.Monthly != 112_50 || g.Late(today) {
		t.Errorf("rythme : %d par mois, en retard %v", g.Monthly, g.Late(today))
	}

	// Dépenses du projet : opérations portant son étiquette.
	for _, amount := range []int64{-80_00, -45_50, 20_00} {
		if _, err := l.CreateTransaction(ctx, ledger.TransactionInput{AccountID: current, Date: "2026-10-20", Label: "Location", Amount: amount, Tags: []string{"Vacances"}}); err != nil {
			t.Fatal(err)
		}
	}
	goals, err := s.Goals(ctx)
	if err != nil || len(goals) != 2 || goals[0].Name != "Vacances" || goals[0].Spent != 125_50 || goals[1].Spent != 0 {
		t.Fatalf("projets : %+v, %v", goals, err)
	}

	// Modification (cagnotte conservée), suppression.
	in.Target = 2_000_00
	if err := s.Update(ctx, id, in); err != nil {
		t.Fatal(err)
	}
	if g, _ := s.Goal(ctx, id); g.Target != 2_000_00 || g.Saved != 300_00 || g.Tag != "vacances" {
		t.Errorf("après modification : %+v", g)
	}
	if err := s.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Goal(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("projet supprimé : %v", err)
	}
	if err := s.Update(ctx, id, in); !errors.Is(err, ErrNotFound) {
		t.Errorf("modification d'un projet supprimé : %v", err)
	}
}

func isInput(err error) bool {
	_, ok := validation.Message(err)
	return ok
}
