package ledger

import (
	"fmt"
	"testing"
)

// searchFixture crée deux comptes et un petit jeu d'opérations variées.
func searchFixture(t *testing.T) (s *Service, courant, livret int64) {
	t.Helper()
	s = newTestService(t)
	courant = mustAccount(t, s, "Courant", 0, "2026-01-01")
	livret = mustAccount(t, s, "Livret", 0, "2026-01-01")
	courses := categoryID(t, s, "Alimentation › Courses")
	restaurants := categoryID(t, s, "Alimentation › Restaurants")
	salaires := categoryID(t, s, "Salaires")
	for _, in := range []TransactionInput{
		{AccountID: courant, Date: "2026-10-01", Label: "CARTE Café de la Gare", Amount: -3_50, CategoryID: restaurants},
		{AccountID: courant, Date: "2026-10-02", Label: "Supermarché Lidl", Amount: -82_10, CategoryID: courses, Cleared: true},
		{AccountID: courant, Date: "2026-10-03", Label: "Boulangerie", Amount: -4_20, Note: "croissants pour le café"},
		{AccountID: courant, Date: "2026-10-05", Label: "Salaire octobre", Amount: 2500_00, CategoryID: salaires, Cleared: true},
		{AccountID: livret, Date: "2026-10-06", Label: "Intérêts", Amount: 12_00},
	} {
		mustTx(t, s, in)
	}
	if _, err := s.CreateTransfer(t.Context(), TransferInput{FromAccountID: courant, ToAccountID: livret, Date: "2026-10-07", Label: "Virement épargne", Amount: 200_00}); err != nil {
		t.Fatal(err)
	}
	return s, courant, livret
}

func labels(p Page) []string {
	out := make([]string, len(p.Items))
	for i, t := range p.Items {
		out[i] = t.Label
	}
	return out
}

func TestSearchFilters(t *testing.T) {
	s, courant, livret := searchFixture(t)
	alimentation := categoryID(t, s, "Alimentation")

	for name, c := range map[string]struct {
		f    Filter
		want string
	}{
		"tout, plus récent d'abord":       {Filter{}, "[Virement épargne Virement épargne Intérêts Salaire octobre Boulangerie Supermarché Lidl CARTE Café de la Gare]"},
		"mot sans accent ni casse":        {Filter{Query: "cafe"}, "[Boulangerie CARTE Café de la Gare]"},
		"préfixe et plusieurs mots":       {Filter{Query: "super lid"}, "[Supermarché Lidl]"},
		"caractères spéciaux neutralisés": {Filter{Query: `"NEAR( -cafe* OR`}, "[]"},
		"compte":                          {Filter{AccountID: livret}, "[Virement épargne Intérêts]"},
		"catégorie et sous-catégories":    {Filter{CategoryID: alimentation}, "[Supermarché Lidl CARTE Café de la Gare]"},
		"non catégorisées hors virements": {Filter{CategoryID: Uncategorized, AccountID: courant}, "[Boulangerie]"},
		"période":                         {Filter{From: "2026-10-02", To: "2026-10-03"}, "[Boulangerie Supermarché Lidl]"},
		"montant absolu":                  {Filter{MinAmount: 4_00, MaxAmount: 100_00}, "[Intérêts Boulangerie Supermarché Lidl]"},
		"pointées":                        {Filter{Cleared: ClearedYes}, "[Salaire octobre Supermarché Lidl]"},
		"tri par montant croissant":       {Filter{AccountID: courant, Sort: SortAmountAsc}, "[Virement épargne Supermarché Lidl Boulangerie CARTE Café de la Gare Salaire octobre]"},
		"tri par montant décroissant":     {Filter{AccountID: livret, Sort: SortAmountDesc}, "[Virement épargne Intérêts]"},
		"tri par date croissante":         {Filter{Cleared: ClearedNo, AccountID: livret, Sort: SortDateAsc}, "[Intérêts Virement épargne]"},
	} {
		t.Run(name, func(t *testing.T) {
			p, err := s.Search(t.Context(), c.f, 0)
			if err != nil {
				t.Fatal(err)
			}
			if got := fmt.Sprint(labels(p)); got != c.want {
				t.Errorf("résultats %s, attendu %s", got, c.want)
			}
		})
	}
}

func TestSearchPagination(t *testing.T) {
	s := newTestService(t)
	account := mustAccount(t, s, "Courant", 0, "2026-01-01")
	for i := range PageSize + 5 {
		mustTx(t, s, TransactionInput{AccountID: account, Date: "2026-10-01", Label: fmt.Sprintf("Op %d", i), Amount: -1_00})
	}
	p, err := s.Search(t.Context(), Filter{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Items) != PageSize || p.Count != PageSize+5 || p.Sum != -int64(PageSize+5)*100 || p.HasPrev() || !p.HasNext() {
		t.Fatalf("page 1 : %d éléments, total %d, somme %d", len(p.Items), p.Count, p.Sum)
	}
	p, _ = s.Search(t.Context(), Filter{}, PageSize)
	if len(p.Items) != 5 || !p.HasPrev() || p.HasNext() {
		t.Errorf("page 2 : %d éléments", len(p.Items))
	}
}
