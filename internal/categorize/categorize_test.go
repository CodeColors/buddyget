package categorize

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/codecolors/buddyget/internal/ledger"
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
	d, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "rules.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	l := ledger.NewService(d)
	account, err := l.CreateAccount(t.Context(), ledger.AccountInput{Name: "Courant", Kind: ledger.AccountCurrent, Bank: ledger.BankSG, OpeningDate: "2026-01-01"})
	if err != nil {
		t.Fatal(err)
	}
	return &env{t: t, s: NewService(d), ledger: l, account: account}
}

func (e *env) category(fullName string) int64 {
	e.t.Helper()
	cats, err := e.ledger.Categories(e.t.Context())
	if err != nil {
		e.t.Fatal(err)
	}
	for _, c := range cats {
		if c.FullName() == fullName {
			return c.ID
		}
	}
	e.t.Fatalf("catégorie %q introuvable", fullName)
	return 0
}

func (e *env) rule(pattern string, match MatchKind, category string, priority int) int64 {
	e.t.Helper()
	id, err := e.s.CreateRule(e.t.Context(), RuleInput{Pattern: pattern, Match: match, CategoryID: e.category(category), Priority: priority})
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *env) tx(label string, category int64) int64 {
	e.t.Helper()
	id, err := e.ledger.CreateTransaction(e.t.Context(), ledger.TransactionInput{
		AccountID: e.account, Date: "2026-10-01", Label: label, Amount: -1_00, CategoryID: category,
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *env) categoryOf(id int64) string {
	e.t.Helper()
	t, err := e.ledger.Transaction(e.t.Context(), id)
	if err != nil {
		e.t.Fatal(err)
	}
	return t.CategoryName
}

func TestMatcherOrderAndKinds(t *testing.T) {
	e := newEnv(t)
	e.rule("carrefour", MatchContains, "Alimentation › Courses", 0)
	e.rule("CARREFOUR MARKET", MatchContains, "Alimentation", 0) // plus précise
	e.rule("cafe", MatchContains, "Alimentation › Restaurants", 0)
	e.rule(`^PRLV SEPA (EDF|ENGIE)`, MatchRegex, "Logement › Énergie", 0)
	e.rule("sncf", MatchContains, "Transport", -5)
	e.rule("billet", MatchContains, "Loisirs › Voyages", 10) // prioritaire

	m, err := e.s.Matcher(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	for label, want := range map[string]string{
		"CB CARREFOUR MARKET 30/10":   "Alimentation",
		"CB Carrefour City":           "Alimentation › Courses",
		"CARTE X4321 CAFÉ DE LA GARE": "Alimentation › Restaurants", // accents ignorés
		"prlv sepa engie":             "Logement › Énergie",         // casse ignorée
		"VIR PRLV SEPA EDF":           "",                           // ^ ancre l'expression
		"SNCF BILLET PARIS LYON":      "Loisirs › Voyages",
		"Librairie":                   "",
	} {
		got := m.Match(label)
		if want == "" && got != 0 || want != "" && got != e.category(want) {
			t.Errorf("Match(%q) = %d, attendu %q", label, got, want)
		}
	}
}

func TestRuleValidation(t *testing.T) {
	e := newEnv(t)
	cat := e.category("Divers")
	for name, c := range map[string]struct {
		in  RuleInput
		msg string
	}{
		"motif vide":         {RuleInput{Pattern: "  ", Match: MatchContains, CategoryID: cat}, "texte"},
		"comparaison":        {RuleInput{Pattern: "x", Match: "egal", CategoryID: cat}, "manière"},
		"regex invalide":     {RuleInput{Pattern: "(café", Match: MatchRegex, CategoryID: cat}, "Expression régulière"},
		"motif trop long":    {RuleInput{Pattern: strings.Repeat("x", patternMaxLen+1), Match: MatchContains, CategoryID: cat}, "dépasser"},
		"catégorie inconnue": {RuleInput{Pattern: "x", Match: MatchContains, CategoryID: 999}, "catégorie"},
		"priorité":           {RuleInput{Pattern: "x", Match: MatchContains, CategoryID: cat, Priority: 101}, "priorité"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := e.s.CreateRule(t.Context(), c.in)
			msg, ok := validation.Message(err)
			if !ok || !strings.Contains(msg, c.msg) {
				t.Errorf("erreur %v, attendu un message contenant %q", err, c.msg)
			}
		})
	}
}

func TestRuleCRUD(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	id := e.rule(" Netflix ", MatchContains, "Loisirs › Abonnements", 0)
	r, err := e.s.Rule(ctx, id)
	if err != nil || r.Pattern != "Netflix" || r.CategoryName != "Loisirs › Abonnements" || r.CategoryColor == "" {
		t.Fatalf("règle lue : %+v, %v", r, err)
	}
	if err := e.s.UpdateRule(ctx, id, RuleInput{Pattern: "NETFLIX.COM", Match: MatchContains, CategoryID: e.category("Divers"), Priority: 3}); err != nil {
		t.Fatal(err)
	}
	if r, _ = e.s.Rule(ctx, id); r.Pattern != "NETFLIX.COM" || r.Priority != 3 || r.CategoryName != "Divers" {
		t.Errorf("règle modifiée : %+v", r)
	}
	if err := e.s.DeleteRule(ctx, id); err != nil {
		t.Fatal(err)
	}
	for _, err := range []error{
		e.s.DeleteRule(ctx, id),
		e.s.UpdateRule(ctx, id, RuleInput{Pattern: "x", Match: MatchContains, CategoryID: e.category("Divers")}),
	} {
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("règle supprimée : %v, attendu ErrNotFound", err)
		}
	}

	// Supprimer la catégorie supprime ses règles.
	id = e.rule("edf", MatchContains, "Logement › Énergie", 0)
	if err := e.ledger.DeleteCategory(ctx, e.category("Logement")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Rule(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("la règle d'une catégorie supprimée doit disparaître : %v", err)
	}
}

func TestApplyRetroactively(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	divers := e.category("Divers")
	sans := e.tx("CB MONOPRIX 12/10", 0)
	deja := e.tx("CB MONOPRIX 13/10", divers)
	autre := e.tx("PHARMACIE", 0)
	transfer, err := e.ledger.CreateTransfer(ctx, ledger.TransferInput{
		FromAccountID: e.account, ToAccountID: e.account2(), Date: "2026-10-01", Label: "MONOPRIX remboursement", Amount: 5_00,
	})
	if err != nil {
		t.Fatal(err)
	}
	courses := e.rule("monoprix", MatchContains, "Alimentation › Courses", 0)

	n, err := e.s.Apply(ctx, courses, false)
	if err != nil || n != 1 {
		t.Fatalf("sans écraser : %d modifiées, %v", n, err)
	}
	if e.categoryOf(sans) != "Alimentation › Courses" || e.categoryOf(deja) != "Divers" || e.categoryOf(autre) != "" || e.categoryOf(transfer) != "" {
		t.Error("seule l'opération sans catégorie doit changer, jamais un virement")
	}
	if n, _ = e.s.Apply(ctx, courses, true); n != 1 || e.categoryOf(deja) != "Alimentation › Courses" {
		t.Errorf("en écrasant : %d modifiées", n)
	}
	if n, _ = e.s.Apply(ctx, courses, true); n != 0 {
		t.Errorf("une seconde application ne doit rien changer : %d", n)
	}

	e.rule("pharma", MatchContains, "Santé › Pharmacie", 0)
	if n, _ = e.s.Apply(ctx, 0, false); n != 1 || e.categoryOf(autre) != "Santé › Pharmacie" {
		t.Errorf("toutes les règles : %d modifiées", n)
	}
	if _, err := e.s.Apply(ctx, 999, false); !errors.Is(err, ErrNotFound) {
		t.Errorf("règle inconnue : %v", err)
	}
}

func (e *env) account2() int64 {
	e.t.Helper()
	id, err := e.ledger.CreateAccount(e.t.Context(), ledger.AccountInput{Name: "Livret", Kind: ledger.AccountSavings, Bank: ledger.BankSG, OpeningDate: "2026-01-01"})
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func TestSuggestPattern(t *testing.T) {
	for label, want := range map[string]string{
		"CARTE X4321 30/10 BOULANGERIE MARTIN 75 PARIS":               "BOULANGERIE MARTIN",
		"PRLV SEPA EDF CLIENTS PARTICULIERS ECH/301026":               "EDF CLIENTS",
		"VIR RECU 1234567 DE: SOCIETE EXEMPLE MOTIF: SALAIRE OCTOBRE": "SOCIETE EXEMPLE",
		"CB CARREFOUR MARKET 30/10":                                   "CARREFOUR MARKET",
		"Café de la Gare":                                             "Café Gare",
		"12345":                                                       "12345",
	} {
		if got := SuggestPattern(label); got != want {
			t.Errorf("SuggestPattern(%q) = %q, attendu %q", label, got, want)
		}
	}
}
