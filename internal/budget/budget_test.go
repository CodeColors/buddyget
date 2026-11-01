package budget

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/platform/db"
	"github.com/codecolors/buddyget/internal/platform/validation"
)

func TestBudgets(t *testing.T) {
	d, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "budget.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	ctx := t.Context()
	l := ledger.NewService(d)
	s := NewService(d, l)

	cats, _ := l.Categories(ctx)
	id := func(name string) int64 {
		i := slices.IndexFunc(cats, func(c ledger.Category) bool { return c.FullName() == name })
		return cats[i].ID
	}
	alim, courses, resto, loisirs := id("Alimentation"), id("Alimentation › Courses"), id("Alimentation › Restaurants"), id("Loisirs")

	for _, in := range []Input{{CategoryID: id("Salaires"), Amount: 100}, {CategoryID: alim, Amount: 0}, {CategoryID: 9999, Amount: 1}} {
		if _, ok := validation.Message(s.Save(ctx, in, "2026-08-15")); !ok {
			t.Errorf("enveloppe invalide acceptée : %+v", in)
		}
	}
	for _, in := range []Input{{alim, 400_00, true}, {resto, 100_00, false}, {loisirs, 50_00, false}} {
		if err := s.Save(ctx, in, "2026-08-15"); err != nil {
			t.Fatal(err)
		}
	}

	account, _ := l.CreateAccount(ctx, ledger.AccountInput{Name: "Courant", Kind: ledger.AccountCurrent, Bank: ledger.BankSG, OpeningDate: "2026-01-01"})
	livret, _ := l.CreateAccount(ctx, ledger.AccountInput{Name: "Livret", Kind: ledger.AccountSavings, Bank: ledger.BankSG, OpeningDate: "2026-01-01"})
	for _, op := range []ledger.TransactionInput{
		{Date: "2026-08-05", Amount: -300_00, CategoryID: courses},
		{Date: "2026-08-06", Amount: -120_00, CategoryID: resto}, // enveloppe Restaurants, pas Alimentation
		{Date: "2026-08-07", Amount: -50_00, CategoryID: alim},
		{Date: "2026-09-05", Amount: -500_00, CategoryID: courses},
		{Date: "2026-09-06", Amount: 20_00, CategoryID: courses}, // remboursement
		{Date: "2026-09-07", Amount: -60_00, CategoryID: loisirs},
		{Date: "2026-09-08", Amount: -999_00}, // sans catégorie
	} {
		op.AccountID, op.Label = account, "op"
		if _, err := l.CreateTransaction(ctx, op); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := l.CreateTransfer(ctx, ledger.TransferInput{FromAccountID: account, ToAccountID: livret, Date: "2026-09-09", Label: "Épargne", Amount: 100_00}); err != nil {
		t.Fatal(err)
	}

	sep, err := s.Month(ctx, "2026-09-10")
	if err != nil || len(sep) != 3 {
		t.Fatalf("septembre : %+v, %v", sep, err)
	}
	type state struct {
		carry, available, spent int64
		level                   Level
	}
	got := map[string]state{}
	for _, st := range sep {
		got[st.CategoryName] = state{st.Carry, st.Available, st.Spent, st.Level(80)}
	}
	want := map[string]state{
		"Alimentation":               {50_00, 450_00, 480_00, Over}, // août : 350 € sur 400, 50 € reportés
		"Alimentation › Restaurants": {0, 100_00, 0, OK},
		"Loisirs":                    {0, 50_00, 60_00, Over},
	}
	for name, w := range want {
		if got[name] != w {
			t.Errorf("%s : %+v, attendu %+v", name, got[name], w)
		}
	}
	oct, _ := s.Month(ctx, "2026-10-31")
	if oct[0].CategoryName != "Alimentation" || oct[0].Carry != -30_00 || oct[0].Available != 370_00 || oct[0].Remaining() != 370_00 {
		t.Errorf("octobre, report cumulé : %+v", oct[0])
	}

	if lv := (Status{Available: 100_00, Spent: 80_00}).Level(80); lv != Risk || lv.Label() != "Bientôt atteint" {
		t.Errorf("seuil atteint : %v", lv)
	}
	if lv := (Status{Available: 100_00, Spent: 79_00}).Level(80); lv != OK {
		t.Errorf("sous le seuil : %v", lv)
	}
	if p := (Status{Available: 0, Spent: 10_00}).Percent(); p != 100 {
		t.Errorf("enveloppe épuisée : %d %%", p)
	}

	hist, err := s.History(ctx, "2026-09-30", 3)
	wantHist := []MonthTotal{{"2026-07-01", 0}, {"2026-08-01", 470_00}, {"2026-09-01", 540_00}}
	if err != nil || !slices.Equal(hist, wantHist) {
		t.Errorf("historique : %+v, %v", hist, err)
	}
	budgets, _ := s.Budgets(ctx)
	if Total(budgets) != 550_00 || budgets[0].Since != "2026-08-01" {
		t.Errorf("enveloppes : %+v", budgets)
	}
	if err := s.Delete(ctx, loisirs); err != nil {
		t.Fatal(err)
	}
	if budgets, _ = s.Budgets(ctx); len(budgets) != 2 {
		t.Errorf("après suppression : %+v", budgets)
	}
}
