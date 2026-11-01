package ledger

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestTransactionLifecycle(t *testing.T) {
	s := newTestService(t)
	ctx := t.Context()
	account := mustAccount(t, s, "Courant", 0, "2026-10-01")
	courses := categoryID(t, s, "Alimentation › Courses")

	id := mustTx(t, s, TransactionInput{AccountID: account, Date: "2026-10-02", Label: "  Marché  ", Amount: -23_40, CategoryID: courses, Note: "légumes"})
	got, err := s.Transaction(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Label != "Marché" || got.AccountName != "Courant" || got.CategoryName != "Alimentation › Courses" || got.CategoryColor == "" {
		t.Errorf("opération lue : %+v", got)
	}

	in := TransactionInput{AccountID: account, Date: "2026-10-03", Label: "Marché bio", Amount: -25_00, Cleared: true}
	if err := s.UpdateTransaction(ctx, id, in); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Transaction(ctx, id)
	if got.Date != "2026-10-03" || got.Amount != -25_00 || !got.Cleared || got.CategoryID != 0 || got.Note != "" {
		t.Errorf("opération modifiée : %+v", got)
	}

	if err := s.DeleteTransaction(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transaction(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("opération supprimée : %v", err)
	}
	if err := s.UpdateTransaction(ctx, id, in); !errors.Is(err, ErrNotFound) {
		t.Errorf("modification d'une opération supprimée : %v", err)
	}
}

func TestTransactionValidation(t *testing.T) {
	s := newTestService(t)
	account := mustAccount(t, s, "Courant", 0, "2026-10-01")
	base := TransactionInput{AccountID: account, Date: "2026-10-02", Label: "Café", Amount: -2_00}
	for name, c := range map[string]struct {
		mutate func(*TransactionInput)
		msg    string
	}{
		"libellé vide":       {func(in *TransactionInput) { in.Label = " " }, "libellé"},
		"libellé trop long":  {func(in *TransactionInput) { in.Label = strings.Repeat("a", labelMaxLen+1) }, "dépasser"},
		"montant nul":        {func(in *TransactionInput) { in.Amount = 0 }, "non nul"},
		"compte inconnu":     {func(in *TransactionInput) { in.AccountID = 999 }, "compte"},
		"catégorie inconnue": {func(in *TransactionInput) { in.CategoryID = 999 }, "Catégorie"},
		"date manquante":     {func(in *TransactionInput) { in.Date = "" }, "date"},
	} {
		t.Run(name, func(t *testing.T) {
			in := base
			c.mutate(&in)
			_, err := s.CreateTransaction(t.Context(), in)
			expectInputError(t, err, c.msg)
		})
	}
}

func TestTransfer(t *testing.T) {
	s := newTestService(t)
	ctx := t.Context()
	courant := mustAccount(t, s, "Courant", 0, "2026-10-01")
	livret := mustAccount(t, s, "Livret", 0, "2026-10-01")

	out, err := s.CreateTransfer(ctx, TransferInput{FromAccountID: courant, ToAccountID: livret, Date: "2026-10-05", Label: "Épargne", Amount: 100_00})
	if err != nil {
		t.Fatal(err)
	}
	leg, _ := s.Transaction(ctx, out)
	if !leg.IsTransfer() || leg.Amount != -100_00 || leg.AccountID != courant || leg.CounterpartAccountID != livret || leg.CounterpartAccountName != "Livret" {
		t.Fatalf("sortie du virement : %+v", leg)
	}

	err = s.UpdateTransaction(ctx, out, TransactionInput{AccountID: courant, Date: "2026-10-05", Label: "x", Amount: -1})
	expectInputError(t, err, "virement")

	// Inverser le sens et changer le montant met à jour les deux jambes.
	if err := s.UpdateTransfer(ctx, leg.TransferID, TransferInput{FromAccountID: livret, ToAccountID: courant, Date: "2026-10-06", Label: "Retrait", Amount: 30_00}); err != nil {
		t.Fatal(err)
	}
	balances, _ := s.Accounts(ctx, "2026-10-31")
	for _, b := range balances {
		want := map[string]int64{"Courant": 30_00, "Livret": -30_00}[b.Name]
		if b.Balance != want {
			t.Errorf("solde %s = %d, attendu %d", b.Name, b.Balance, want)
		}
	}

	_, err = s.CreateTransfer(ctx, TransferInput{FromAccountID: courant, ToAccountID: courant, Date: "2026-10-05", Label: "x", Amount: 1})
	expectInputError(t, err, "deux comptes différents")
	_, err = s.CreateTransfer(ctx, TransferInput{FromAccountID: courant, ToAccountID: livret, Date: "2026-10-05", Label: "x", Amount: -5})
	expectInputError(t, err, "positif")

	// Supprimer une jambe supprime le virement entier.
	if err := s.DeleteTransaction(ctx, out); err != nil {
		t.Fatal(err)
	}
	page, _ := s.Search(ctx, Filter{}, 0)
	if page.Count != 0 {
		t.Errorf("%d opérations restantes après suppression du virement", page.Count)
	}
	// L'index plein texte suit les suppressions en cascade.
	if _, err := s.db.W.ExecContext(ctx, `INSERT INTO transactions_fts (transactions_fts, rank) VALUES ('integrity-check', 1)`); err != nil {
		t.Errorf("index plein texte désynchronisé : %v", err)
	}
}

func TestRecentLabelsAndLastAccount(t *testing.T) {
	s := newTestService(t)
	ctx := t.Context()
	if id, err := s.LastUsedAccount(ctx); err != nil || id != 0 {
		t.Errorf("LastUsedAccount sur base vide = %d, %v", id, err)
	}
	a := mustAccount(t, s, "A", 0, "2026-10-01")
	b := mustAccount(t, s, "B", 0, "2026-10-01")
	for _, l := range []string{"Boulangerie", "Café", "Boulangerie", "Essence"} {
		mustTx(t, s, TransactionInput{AccountID: a, Date: "2026-10-02", Label: l, Amount: -1_00})
	}
	mustTx(t, s, TransactionInput{AccountID: b, Date: "2026-10-01", Label: "Loyer", Amount: -700_00})

	labels, err := s.RecentLabels(ctx, 3)
	if err != nil || !slices.Equal(labels, []string{"Loyer", "Essence", "Boulangerie"}) {
		t.Errorf("RecentLabels = %v, %v", labels, err)
	}
	if id, _ := s.LastUsedAccount(ctx); id != b {
		t.Errorf("LastUsedAccount = %d, attendu %d", id, b)
	}
	if err := s.SetAccountArchived(ctx, b, true); err != nil {
		t.Fatal(err)
	}
	if id, _ := s.LastUsedAccount(ctx); id != a {
		t.Errorf("un compte archivé ne doit pas être proposé : %d", id)
	}
}
