package scenarios

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/loans"
	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/platform/db"
	"github.com/codecolors/buddyget/internal/platform/validation"
	"github.com/codecolors/buddyget/internal/recurring"
)

func TestApply(t *testing.T) {
	for _, c := range []struct {
		it     Item
		amount int64
		want   int64
	}{
		{Item{Percent: 1_000}, 2_000_00, 2_200_00},  // salaire + 10 %
		{Item{Percent: -2_500}, -80_00, -60_00},     // dépense − 25 %
		{Item{Percent: 333}, 1_00, 1_03},            // arrondi au centime
		{Item{Amount: 50_00}, -700_00, -750_00},     // loyer + 50 €
		{Item{Amount: -100_00}, 2_000_00, 1_900_00}, // salaire − 100 €
		{Item{Amount: -900_00}, -700_00, 0},         // baisse au-delà : annulée
		{Item{Percent: -10_000}, 2_000_00, 0},
	} {
		if got := c.it.Apply(c.amount); got != c.want {
			t.Errorf("%+v sur %d : %d, attendu %d", c.it, c.amount, got, c.want)
		}
	}
}

type env struct {
	t         *testing.T
	s         *Service
	ledger    *ledger.Service
	recurring *recurring.Service
	loans     *loans.Service
	account   int64
}

func newEnv(t *testing.T) *env {
	d, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "scenarios.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	l, r := ledger.NewService(d), recurring.NewService(d)
	lo := loans.NewService(d, r)
	account, err := l.CreateAccount(t.Context(), ledger.AccountInput{Name: "Courant", Kind: ledger.AccountCurrent, Bank: ledger.BankSG, OpeningDate: "2026-01-01"})
	if err != nil {
		t.Fatal(err)
	}
	return &env{t: t, s: NewService(d, l, r, lo), ledger: l, recurring: r, loans: lo, account: account}
}

func (e *env) rule(label string, amount int64, start calendar.Date) int64 {
	e.t.Helper()
	id, err := e.recurring.CreateRule(e.t.Context(), recurring.RuleInput{Label: label, AccountID: e.account, Amount: amount,
		Period: calendar.Monthly, Shift: calendar.NextBusinessDay, Start: start, Origin: recurring.Manual})
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func TestScenarioService(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	rule := e.rule("Loyer", -700_00, "2026-01-05")
	for _, bad := range []string{"", "   "} {
		if _, err := e.s.Create(ctx, bad); !isInput(err) {
			t.Errorf("nom %q accepté", bad)
		}
	}
	id, err := e.s.Create(ctx, "Déménagement")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Create(ctx, "déménagement"); !isInput(err) {
		t.Error("nom en double accepté")
	}

	for _, bad := range []Item{
		{Kind: "inconnu", Date: "2027-01-01"},
		{Kind: OneOff, AccountID: e.account, Label: "Vélo", Amount: -800_00},
		{Kind: OneOff, AccountID: e.account, Date: "2027-01-01", Amount: -800_00},
		{Kind: OneOff, AccountID: e.account, Date: "2027-01-01", Label: "Vélo"},
		{Kind: OneOff, Date: "2027-01-01", Label: "Vélo", Amount: -800_00},
		{Kind: OneOff, AccountID: 999, Date: "2027-01-01", Label: "Vélo", Amount: -800_00},
		{Kind: Repeating, AccountID: e.account, Date: "2027-01-01", Label: "Piano", Amount: -60_00, Months: 25},
		{Kind: Repeating, AccountID: e.account, Date: "2027-01-01", Label: "Piano", Amount: -60_00, Months: 1, End: "2026-12-01"},
		{Kind: Suspend, Date: "2027-01-01"},
		{Kind: Suspend, RuleID: 999, Date: "2027-01-01"},
		{Kind: Vary, RuleID: rule, Date: "2027-01-01"},
		{Kind: Vary, RuleID: rule, Date: "2027-01-01", Amount: 10_00, Percent: 500},
		{Kind: Vary, RuleID: rule, Date: "2027-01-01", Percent: -10_001},
		{Kind: NewLoan, AccountID: e.account, Date: "2027-01-01", Label: "Moto", Amount: 6_000_00},
		{Kind: Prepay, Date: "2027-01-01", Amount: 1_000_00},
		{Kind: Prepay, LoanID: 999, Date: "2027-01-01", Amount: 1_000_00},
	} {
		if _, err := e.s.AddItem(ctx, id, bad); !isInput(err) {
			t.Errorf("modification invalide acceptée : %+v (%v)", bad, err)
		}
	}
	if _, err := e.s.AddItem(ctx, 999, Item{Kind: Suspend, RuleID: rule, Date: "2027-01-01"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("scénario inconnu : %v", err)
	}

	// Seuls les champs utiles à la nature sont gardés.
	item, err := e.s.AddItem(ctx, id, Item{Kind: Suspend, RuleID: rule, Date: "2027-01-01", Label: "ignoré", Amount: 5, AccountID: e.account})
	if err != nil {
		t.Fatal(err)
	}
	sc, err := e.s.Scenario(ctx, id)
	if err != nil || len(sc.Items) != 1 || sc.Items[0] != (Item{ID: item, Kind: Suspend, RuleID: rule, RuleLabel: "Loyer", Date: "2027-01-01"}) {
		t.Fatalf("scénario : %+v, %v", sc, err)
	}
	if sc.Items[0].Title() != "Arrêt : Loyer" {
		t.Errorf("titre : %s", sc.Items[0].Title())
	}

	if err := e.s.Rename(ctx, id, "Nouveau logement"); err != nil {
		t.Fatal(err)
	}
	if list, _ := e.s.Scenarios(ctx); len(list) != 1 || list[0].Name != "Nouveau logement" || len(list[0].Items) != 1 {
		t.Errorf("scénarios : %+v", list)
	}
	if err := e.s.DeleteItem(ctx, id+1, item); !errors.Is(err, ErrNotFound) {
		t.Errorf("modification d'un autre scénario : %v", err)
	}
	if err := e.s.DeleteItem(ctx, id, item); err != nil {
		t.Fatal(err)
	}
	if err := e.s.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Scenario(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("scénario supprimé : %v", err)
	}
}

func TestConvert(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	salary := e.rule("Salaire", 2_000_00, "2026-08-31")
	streaming := e.rule("Streaming", -15_00, "2026-08-10")
	future := e.rule("Club", -30_00, "2027-03-01")
	gym := e.rule("Salle", -40_00, "2027-02-01")
	loan, err := e.loans.Create(ctx, loans.Input{Name: "Auto", AccountID: e.account, Params: loans.Params{Principal: 12_000_00, Months: 12, First: "2026-11-05"}})
	if err != nil {
		t.Fatal(err)
	}
	id, _ := e.s.Create(ctx, "Tout réel")
	for _, it := range []Item{
		{Kind: OneOff, AccountID: e.account, Date: "2026-12-15", Label: "Vélo", Amount: -800_00},
		{Kind: Repeating, AccountID: e.account, Date: "2026-12-20", Label: "Piano", Amount: -60_00, Months: 1, End: "2027-06-30"},
		{Kind: Suspend, RuleID: streaming, Date: "2026-12-01"},
		{Kind: Suspend, RuleID: future, Date: "2027-01-01"},              // avant sa première échéance
		{Kind: Vary, RuleID: salary, Date: "2026-11-15", Amount: 100_00}, // reprend un 31
		{Kind: Vary, RuleID: gym, Date: "2027-01-01", Percent: 5_000},    // dès la première échéance
		{Kind: NewLoan, AccountID: e.account, Date: "2027-01-05", Label: "Moto", Amount: 6_000_00, Months: 6, Rate: 2_000},
		{Kind: Prepay, LoanID: loan, Date: "2027-02-20", Amount: 3_000_00},
	} {
		if _, err := e.s.AddItem(ctx, id, it); err != nil {
			t.Fatalf("%+v : %v", it, err)
		}
	}
	if err := e.s.Convert(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Scenario(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Error("le scénario converti disparaît")
	}

	page, err := e.ledger.Search(ctx, ledger.Filter{}, 0)
	if err != nil || len(page.Items) != 1 || page.Items[0].Label != "Vélo" || page.Items[0].Date != "2026-12-15" {
		t.Errorf("opération planifiée : %+v, %v", page, err)
	}
	rules, _ := e.recurring.Rules(ctx)
	byLabel := map[string][]recurring.Rule{}
	for _, r := range rules {
		byLabel[r.Label] = append(byLabel[r.Label], r)
	}
	if p := byLabel["Piano"]; len(p) != 1 || p[0].Amount != -60_00 || p[0].Start != "2026-12-20" || p[0].End != "2027-06-30" {
		t.Errorf("récurrence ajoutée : %+v", p)
	}
	if s := byLabel["Streaming"]; len(s) != 1 || s[0].End != "2026-11-30" || !s[0].Active {
		t.Errorf("récurrence arrêtée : %+v", s)
	}
	if c := byLabel["Club"]; len(c) != 1 || c[0].Active {
		t.Errorf("récurrence arrêtée avant de commencer : %+v", c)
	}
	if s := byLabel["Salaire"]; len(s) != 2 || s[0].End != "2026-12-30" || s[1].Start != "2026-12-31" || s[1].Amount != 2_100_00 || s[1].End != "" {
		t.Errorf("salaire scindé : %+v", s)
	}
	if g := byLabel["Salle"]; len(g) != 1 || g[0].Amount != -60_00 || g[0].Start != "2027-02-01" {
		t.Errorf("variation dès la première échéance : %+v", g)
	}
	list, _ := e.loans.Loans(ctx)
	if len(list) != 2 || list[0].Name != "Auto" || len(list[0].Prepayments) != 1 || list[1].Name != "Moto" || list[1].Rate != 2_000 {
		t.Errorf("crédits : %+v", list)
	}
	if m := byLabel["Crédit Moto"]; len(m) != 1 {
		t.Errorf("mensualité du nouveau crédit : %+v", m)
	}
}

func isInput(err error) bool {
	_, ok := validation.Message(err)
	return ok
}
