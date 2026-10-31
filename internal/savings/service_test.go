package savings

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/platform/db"
	"github.com/codecolors/buddyget/internal/platform/validation"
)

type env struct {
	t      *testing.T
	s      *Service
	ledger *ledger.Service
}

func newEnv(t *testing.T) *env {
	t.Helper()
	d, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "savings.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	l := ledger.NewService(d)
	return &env{t: t, s: NewService(d, l), ledger: l}
}

func (e *env) account(name string, kind ledger.AccountKind, opening int64, date calendar.Date) int64 {
	e.t.Helper()
	id, err := e.ledger.CreateAccount(e.t.Context(), ledger.AccountInput{Name: name, Kind: kind, Bank: ledger.BankOther, OpeningBalance: opening, OpeningDate: date})
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func isInputError(err error) bool {
	_, ok := validation.Message(err)
	return ok
}

func TestSettingsAndInterest(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	courant := e.account("Courant", ledger.AccountCurrent, 0, "2024-01-01")
	livret := e.account("Livret A", ledger.AccountSavings, 1_000_00, "2024-01-01")

	st := Settings{AccountID: courant, Product: LivretA, Rate: 300, Method: Fortnight, AutoInterest: true}
	if err := e.s.Save(ctx, st, "2025-03-01"); !isInputError(err) {
		t.Errorf("réglages d'épargne sur un compte courant : %v", err)
	}
	st.AccountID = livret
	if err := e.s.Save(ctx, st, "2025-03-01"); err != nil {
		t.Fatal(err)
	}
	st.Ceiling = 22_950_00
	if err := e.s.Save(ctx, st, "2026-01-15"); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := e.s.Settings(ctx, livret); err != nil || !ok || got.Ceiling != 22_950_00 || got.Since != "2025-03-01" {
		t.Fatalf("réglages relus : %+v, %v, %v", got, ok, err)
	}

	// Inscription automatique à partir de 2025, année d'enregistrement des
	// réglages : 2024 reste à inscrire à la demande.
	if n, err := e.s.CloseYears(ctx, "2026-10-31"); err != nil || n != 1 {
		t.Fatalf("années closes : %d, %v", n, err)
	}
	if n, _ := e.s.CloseYears(ctx, "2026-10-31"); n != 0 {
		t.Errorf("rien de plus à inscrire : %d", n)
	}
	page, err := e.ledger.Search(ctx, ledger.Filter{AccountID: livret}, 0)
	if err != nil || page.Count != 1 || page.Items[0].Label != "Intérêts 2025" || page.Items[0].Date != "2025-12-31" ||
		page.Items[0].Amount != 30_00 || page.Items[0].InterestYear != 2025 {
		t.Fatalf("écriture d'intérêts : %+v, %v", page.Items, err)
	}

	sums, err := e.s.Overview(ctx, "2026-10-31")
	if err != nil || len(sums) != 1 {
		t.Fatalf("vue épargne : %+v, %v", sums, err)
	}
	s := sums[0]
	// 2026 : 1 030 € au 1er janvier, 3 %, vingt quinzaines achevées au 31 octobre.
	if s.Account.Balance != 1_030_00 || s.InterestPaid != 30_00 || s.Accrued != 25_75 || s.YearEstimate != 30_90 {
		t.Errorf("synthèse : solde %d, intérêts %d, acquis %d, estimés %d", s.Account.Balance, s.InterestPaid, s.Accrued, s.YearEstimate)
	}
	if !slices.Equal(s.Missing, []YearInterest{{2024, 30_00}}) {
		t.Errorf("années à inscrire : %+v", s.Missing)
	}
	if room := s.CeilingRoom(); room != 21_920_00 {
		t.Errorf("reste sous le plafond : %d", room)
	}

	if err := e.s.RecordInterest(ctx, livret, 2024, "2026-10-31"); err != nil {
		t.Fatal(err)
	}
	for _, year := range []int{2024, 2026} {
		if err := e.s.RecordInterest(ctx, livret, year, "2026-10-31"); !isInputError(err) {
			t.Errorf("intérêts %d : %v", year, err)
		}
	}
	// Déplacée vers un autre compte, l'écriture redevient ordinaire.
	page, _ = e.ledger.Search(ctx, ledger.Filter{AccountID: livret, Query: "2025"}, 0)
	moved := page.Items[0]
	if err := e.ledger.UpdateTransaction(ctx, moved.ID, ledger.TransactionInput{AccountID: courant, Date: moved.Date, Label: moved.Label, Amount: moved.Amount}); err != nil {
		t.Fatal(err)
	}
	if tx, _ := e.ledger.Transaction(ctx, moved.ID); tx.InterestYear != 0 {
		t.Errorf("écriture déplacée : %+v", tx)
	}

	if err := e.s.Remove(ctx, livret); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := e.s.Settings(ctx, livret); ok {
		t.Error("réglages supprimés")
	}
}

func TestAvailabilityService(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	pel := e.account("PEL", ledger.AccountSavings, 5_000_00, "2024-05-01")
	if _, _, ok, err := e.s.Availability(ctx, pel, "2026-10-31"); ok || err != nil {
		t.Errorf("sans réglages : %v, %v", ok, err)
	}
	st := Settings{AccountID: pel, Product: PEL, Method: Compound, Rate: 200, UnlockDate: "2028-05-01", Penalty: 100}
	if err := e.s.Save(ctx, st, "2026-10-31"); err != nil {
		t.Fatal(err)
	}
	a, got, ok, err := e.s.Availability(ctx, pel, "2026-10-31")
	if err != nil || !ok || a != (Availability{5_000_00, 0, 5_000_00, "2028-05-01"}) || got.Penalty != 100 {
		t.Errorf("PEL bloqué : %+v, %+v, %v", a, got, err)
	}
}

func TestHistory(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	a := e.account("Livret A", ledger.AccountSavings, 1_000_00, "2026-08-15")
	e.account("LDDS", ledger.AccountSavings, 500_00, "2026-10-01")
	e.account("Courant", ledger.AccountCurrent, 9_999_00, "2026-01-01")
	for _, in := range []ledger.TransactionInput{
		{AccountID: a, Date: "2026-09-10", Label: "Versement", Amount: 200_00},
		{AccountID: a, Date: "2026-10-20", Label: "Retrait", Amount: -50_00},
		{AccountID: a, Date: "2026-11-05", Label: "Futur", Amount: 1_00},
	} {
		if _, err := e.ledger.CreateTransaction(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	got, err := e.s.History(ctx, "2026-10-31", 24)
	want := []Balance{{"2026-08-31", 1_000_00}, {"2026-09-30", 1_200_00}, {"2026-10-31", 1_650_00}}
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("historique : %+v, %v", got, err)
	}
	if got, _ := e.s.History(ctx, "2026-10-31", 1); len(got) != 2 || got[0].Date != "2026-09-30" {
		t.Errorf("deux derniers points : %+v", got)
	}
	if got, err := newEnv(t).s.History(ctx, "2026-10-31", 24); err != nil || got != nil {
		t.Errorf("sans épargne : %+v, %v", got, err)
	}
}
