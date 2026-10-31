package recurring

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/platform/db"
	"github.com/codecolors/buddyget/internal/platform/validation"
)

type env struct {
	t       *testing.T
	s       *Service
	ledger  *ledger.Service
	account int64
}

func newEnv(t *testing.T) *env {
	t.Helper()
	d, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "recurring.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	l := ledger.NewService(d)
	account, err := l.CreateAccount(t.Context(), ledger.AccountInput{Name: "Courant", Kind: ledger.AccountCurrent, Bank: ledger.BankSG, OpeningDate: "2024-01-01"})
	if err != nil {
		t.Fatal(err)
	}
	return &env{t: t, s: NewService(d), ledger: l, account: account}
}

func (e *env) monthlyOps(label string, amount int64, n int, start calendar.Date) {
	e.t.Helper()
	for i := range n {
		if _, err := e.ledger.CreateTransaction(e.t.Context(), ledger.TransactionInput{
			AccountID: e.account, Date: calendar.Monthly.Nth(start, i, false), Label: label, Amount: amount,
		}); err != nil {
			e.t.Fatal(err)
		}
	}
}

func TestRuleCRUDAndNext(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	in := RuleInput{Label: " Loyer ", AccountID: e.account, Amount: -700_00, Period: calendar.Monthly, Start: "2026-01-31", End: "2026-12-31", Origin: Manual}
	id, err := e.s.CreateRule(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	r, err := e.s.Rule(ctx, id)
	if err != nil || r.Label != "Loyer" || r.AccountName != "Courant" || !r.Active || r.Origin != Manual {
		t.Fatalf("récurrence lue : %+v, %v", r, err)
	}
	if next, ok := r.Next("2026-02-01"); !ok || next != "2026-02-28" {
		t.Errorf("prochaine échéance : %s, %v", next, ok)
	}
	if _, ok := r.Next("2027-01-01"); ok {
		t.Error("après la date de fin, la récurrence est terminée")
	}

	in.Period, in.LastDay, in.End = calendar.Quarterly, true, ""
	if err := e.s.UpdateRule(ctx, id, in); err != nil {
		t.Fatal(err)
	}
	if r, _ = e.s.Rule(ctx, id); r.Period != calendar.Quarterly || !r.LastDay || r.End != "" {
		t.Errorf("récurrence modifiée : %+v", r)
	}
	if err := e.s.SetActive(ctx, id, false); err != nil {
		t.Fatal(err)
	}
	rules, _ := e.s.Rules(ctx)
	if len(rules) != 1 || rules[0].Active {
		t.Errorf("récurrence suspendue : %+v", rules)
	}
	if err := e.s.DeleteRule(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Rule(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("récurrence supprimée : %v", err)
	}
}

func TestRuleValidation(t *testing.T) {
	e := newEnv(t)
	base := RuleInput{Label: "Loyer", AccountID: e.account, Amount: -700_00, Period: calendar.Monthly, Start: "2026-01-05", Origin: Manual}
	for name, c := range map[string]struct {
		mutate func(*RuleInput)
		msg    string
	}{
		"libellé vide": {func(in *RuleInput) { in.Label = " " }, "libellé"},
		"montant nul":  {func(in *RuleInput) { in.Amount = 0 }, "non nul"},
		"période":      {func(in *RuleInput) { in.Period = calendar.Period{Count: 30, Unit: calendar.Month} }, "Périodicité"},
		"fin avant":    {func(in *RuleInput) { in.End = "2025-12-31" }, "fin"},
		"sans début":   {func(in *RuleInput) { in.Start = "" }, "première échéance"},
		"compte":       {func(in *RuleInput) { in.AccountID = 999 }, "compte"},
		"catégorie":    {func(in *RuleInput) { in.CategoryID = 999 }, "Catégorie"},
	} {
		t.Run(name, func(t *testing.T) {
			in := base
			c.mutate(&in)
			_, err := e.s.CreateRule(t.Context(), in)
			if msg, ok := validation.Message(err); !ok || !strings.Contains(msg, c.msg) {
				t.Errorf("erreur %v, attendu %q", err, c.msg)
			}
		})
	}
	in := base
	in.Period, in.LastDay = calendar.Weekly, true
	id, err := e.s.CreateRule(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	if r, _ := e.s.Rule(t.Context(), id); r.LastDay {
		t.Error("« dernier jour du mois » ne vaut que pour une périodicité en mois")
	}
}

func TestDetectedAcceptAndDismiss(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	const today calendar.Date = "2026-10-31"
	e.monthlyOps("PRLV SEPA EDF", -78_32, 8, "2026-03-05")
	e.monthlyOps("NETFLIX.COM", -17_99, 6, "2026-05-12")
	e.monthlyOps("ABONNEMENT SALLE", -29_90, 6, "2026-05-20")

	cs, err := e.s.Detected(ctx, today)
	if err != nil || len(cs) != 3 {
		t.Fatalf("%d séries détectées, attendu 3 (%v)", len(cs), err)
	}
	edf := cs[0]
	for _, c := range cs {
		if c.Label == "PRLV SEPA EDF" {
			edf = c
		}
	}

	id, err := e.s.Accept(ctx, edf.Signature, today)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := e.s.Rule(ctx, id)
	if r.Origin != Detected || r.Start != "2026-10-05" || r.Amount != -78_32 || r.Period != calendar.Monthly {
		t.Errorf("récurrence validée : %+v", r)
	}

	// La série validée et la série rejetée ne sont plus proposées ; une
	// récurrence saisie à la main couvre aussi sa série.
	var netflix string
	for _, c := range cs {
		if c.Label == "NETFLIX.COM" {
			netflix = c.Signature
		}
	}
	if err := e.s.Dismiss(ctx, netflix); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.CreateRule(ctx, RuleInput{Label: "Abonnement salle", AccountID: e.account, Amount: -29_90, Period: calendar.Monthly, Start: "2026-10-20", Origin: Manual}); err != nil {
		t.Fatal(err)
	}
	// Une série validée après modification de son libellé reste couverte.
	if err := e.s.UpdateRule(ctx, id, RuleInput{Label: "Électricité", AccountID: e.account, Amount: -80_00, Period: calendar.Monthly, Start: r.Start}); err != nil {
		t.Fatal(err)
	}
	if cs, _ = e.s.Detected(ctx, today); len(cs) != 0 {
		t.Errorf("plus aucune série ne doit être proposée : %+v", cs)
	}
	if _, err := e.s.Accept(ctx, netflix, today); !errors.Is(err, ErrNotFound) {
		t.Errorf("une série rejetée ne peut plus être validée : %v", err)
	}
}
