package ledger

import (
	"errors"
	"testing"
)

func TestDefaultCategoriesOrder(t *testing.T) {
	s := newTestService(t)
	cats, err := s.Categories(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	groups := GroupByKind(cats)
	if len(groups) != 3 || groups[0].Kind != CategoryExpense || groups[2].Kind != CategoryTransfer {
		t.Fatalf("groupes inattendus : %+v", groups)
	}
	first := groups[0].Categories
	if first[0].FullName() != "Alimentation" || first[1].FullName() != "Alimentation › Courses" {
		t.Errorf("début des dépenses : %s, %s", first[0].FullName(), first[1].FullName())
	}
	// « Énergie » se range parmi les E, avant « Internet », malgré l'accent.
	var logement []string
	for _, c := range first {
		if c.ParentName == "Logement" {
			logement = append(logement, c.Name)
		}
	}
	if len(logement) < 3 || logement[1] != "Eau" || logement[2] != "Énergie" {
		t.Errorf("sous-catégories de Logement : %v", logement)
	}
}

func TestCategoryHierarchy(t *testing.T) {
	s := newTestService(t)
	ctx := t.Context()
	parent, err := s.CreateCategory(ctx, CategoryInput{Name: "Animaux", Kind: CategoryExpense, Color: "#AABBCC"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := s.CreateCategory(ctx, CategoryInput{ParentID: parent, Name: "Vétérinaire", Kind: CategoryIncome})
	if err != nil {
		t.Fatal(err)
	}
	c, _ := s.Category(ctx, child)
	if c.Kind != CategoryExpense || c.Color != DefaultColor || c.FullName() != "Animaux › Vétérinaire" {
		t.Errorf("sous-catégorie : %+v", c)
	}
	if p, _ := s.Category(ctx, parent); p.Color != "#aabbcc" {
		t.Errorf("couleur non normalisée : %q", p.Color)
	}

	_, err = s.CreateCategory(ctx, CategoryInput{ParentID: child, Name: "Vaccins"})
	expectInputError(t, err, "ne peut pas contenir")
	_, err = s.CreateCategory(ctx, CategoryInput{ParentID: parent, Name: "vétérinaire"})
	expectInputError(t, err, "déjà ce nom")
	err = s.UpdateCategory(ctx, parent, CategoryInput{ParentID: categoryID(t, s, "Loisirs"), Name: "Animaux"})
	expectInputError(t, err, "a des sous-catégories")
	err = s.UpdateCategory(ctx, parent, CategoryInput{ParentID: parent, Name: "Animaux"})
	expectInputError(t, err, "sa propre parente")

	// Le type d'un parent est reporté sur ses enfants.
	if err := s.UpdateCategory(ctx, parent, CategoryInput{Name: "Animaux", Kind: CategoryIncome, Color: "#aabbcc"}); err != nil {
		t.Fatal(err)
	}
	if c, _ := s.Category(ctx, child); c.Kind != CategoryIncome {
		t.Errorf("type de l'enfant = %s, attendu revenu", c.Kind)
	}
}

func TestDeleteCategory(t *testing.T) {
	s := newTestService(t)
	ctx := t.Context()
	account := mustAccount(t, s, "Courant", 0, "2026-10-01")
	santé := categoryID(t, s, "Santé")
	pharmacie := categoryID(t, s, "Santé › Pharmacie")
	tx := mustTx(t, s, TransactionInput{AccountID: account, Date: "2026-10-02", Label: "Pharmacie", Amount: -12_00, CategoryID: pharmacie})

	if err := s.DeleteCategory(ctx, santé); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Category(ctx, pharmacie); !errors.Is(err, ErrNotFound) {
		t.Error("les sous-catégories doivent être supprimées avec leur parent")
	}
	got, _ := s.Transaction(ctx, tx)
	if got.CategoryID != 0 || got.CategoryName != "" {
		t.Errorf("l'opération doit devenir non catégorisée : %+v", got)
	}
}
