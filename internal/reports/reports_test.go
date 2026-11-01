package reports

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/platform/db"
)

func TestReports(t *testing.T) {
	d, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "reports.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	ctx := t.Context()
	l := ledger.NewService(d)
	s := NewService(d, l)
	cats, _ := l.Categories(ctx)
	cat := func(name string) int64 {
		return cats[slices.IndexFunc(cats, func(c ledger.Category) bool { return c.FullName() == name })].ID
	}
	courant, _ := l.CreateAccount(ctx, ledger.AccountInput{Name: "Courant", Kind: ledger.AccountCurrent, Bank: ledger.BankSG, OpeningDate: "2026-01-01"})
	livret, _ := l.CreateAccount(ctx, ledger.AccountInput{Name: "Livret", Kind: ledger.AccountSavings, Bank: ledger.BankSG, OpeningDate: "2026-01-01"})
	courses, salaires := cat("Alimentation › Courses"), cat("Salaires")
	for _, in := range []ledger.TransactionInput{
		{Date: "2026-09-28", Label: "SALAIRE", Amount: 1900_00, CategoryID: salaires},
		{Date: "2026-09-10", Label: "Hypermarché", Amount: -200_00, CategoryID: courses},
		{Date: "2026-10-28", Label: "SALAIRE", Amount: 2000_00, CategoryID: salaires},
		{Date: "2026-10-03", Label: "Hypermarché", Amount: -300_00, CategoryID: courses},
		{Date: "2026-10-12", Label: "CB CARREFOUR 12/10", Amount: -50_00, CategoryID: courses},
		{Date: "2026-10-25", Label: "CB CARREFOUR 25/10", Amount: -30_00, CategoryID: courses},
		{Date: "2026-10-14", Label: "Remboursement", Amount: 20_00, CategoryID: courses},
		{Date: "2026-10-15", Label: "Restaurant", Amount: -40_00, CategoryID: cat("Alimentation › Restaurants")},
		{Date: "2026-10-16", Label: "Concert", Amount: -60_00, CategoryID: cat("Loisirs"), Tags: []string{"vacances"}},
		{Date: "2026-10-17", Label: "DIVERS", Amount: -10_00},
		{Date: "2026-10-18", Label: "Vente", Amount: 5_00},
		{Date: "2026-10-19", Label: "Vers livret externe", Amount: -100_00, CategoryID: cat("Épargne")},
		{Date: "2026-11-02", Label: "Novembre", Amount: -1_00},
	} {
		in.AccountID = courant
		if _, err := l.CreateTransaction(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := l.CreateTransfer(ctx, ledger.TransferInput{FromAccountID: courant, ToAccountID: livret, Date: "2026-10-20", Label: "Épargne", Amount: 500_00}); err != nil {
		t.Fatal(err)
	}

	r, err := s.Monthly(ctx, "2026-10-31")
	if err != nil {
		t.Fatal(err)
	}
	if r.From != "2026-10-01" || r.To != "2026-10-31" || r.Income != 2005_00 || r.Expenses != 470_00 || r.Saved() != 1535_00 {
		t.Errorf("totaux : %+v", r)
	}
	if rate := r.SavingsRate(); rate != 77 {
		t.Errorf("taux d'épargne : %d", rate)
	}
	if !r.HasPrevious || r.Previous != (Totals{Income: 1900_00, Expenses: 200_00}) {
		t.Errorf("mois précédent : %+v", r.Previous)
	}
	type catView struct {
		name             string
		amount, previous int64
		share            int
	}
	var gotCats []catView
	for _, c := range r.Categories {
		gotCats = append(gotCats, catView{c.Name, c.Amount, c.Previous, c.Share})
	}
	wantCats := []catView{{"Alimentation", 400_00, 200_00, 85}, {"Loisirs", 60_00, 0, 13}, {"", 10_00, 0, 2}}
	if !slices.Equal(gotCats, wantCats) {
		t.Errorf("catégories : %+v", gotCats)
	}
	if change := r.Categories[0].Change(); change != 100 {
		t.Errorf("évolution : %d", change)
	}
	if change := r.Categories[1].Change(); change != 0 {
		t.Error("pas d'évolution sans dépense précédente")
	}
	wantMerchants := []Merchant{{"Hypermarché", 300_00, 1}, {"CB CARREFOUR 25/10", 80_00, 2}, {"Concert", 60_00, 1}, {"Restaurant", 40_00, 1}, {"DIVERS", 10_00, 1}}
	if !slices.Equal(r.Merchants, wantMerchants) {
		t.Errorf("commerçants : %+v", r.Merchants)
	}
	if !slices.Equal(r.Tags, []TagLine{{Name: "vacances", Expenses: 60_00, Count: 1}}) || r.Months != nil {
		t.Errorf("étiquettes : %+v", r.Tags)
	}

	y, err := s.Annual(ctx, 2026)
	if err != nil {
		t.Fatal(err)
	}
	if len(y.Months) != 12 || y.Months[8] != (MonthLine{"2026-09-01", 1900_00, 200_00}) || y.Months[9].Expenses != 470_00 ||
		y.Months[10].Expenses != 1_00 || y.Expenses != 671_00 || y.HasPrevious {
		t.Errorf("rapport annuel : %+v", y)
	}
	if empty, _ := s.Monthly(ctx, "2025-01-15"); empty.Income != 0 || len(empty.Categories) != 0 {
		t.Errorf("mois vide : %+v", empty)
	}
	if (Report{}).SavingsRate() != 0 {
		t.Error("pas de taux sans revenus")
	}
}
