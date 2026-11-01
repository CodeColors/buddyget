package ledger

import (
	"slices"
	"testing"
)

func TestParseTags(t *testing.T) {
	got, err := ParseTags(" vacances  2027, déductible,, Vacances 2027 ,DÉDUCTIBLE ")
	if err != nil || !slices.Equal(got, []string{"vacances 2027", "déductible"}) {
		t.Errorf("étiquettes : %q, %v", got, err)
	}
	if _, err := ParseTags("une étiquette beaucoup trop longue pour être raisonnable"); err == nil {
		t.Error("étiquette trop longue acceptée")
	}
}

func TestTags(t *testing.T) {
	s := newTestService(t)
	ctx := t.Context()
	a := mustAccount(t, s, "Courant", 0, "2026-01-01")
	b := mustAccount(t, s, "Livret", 0, "2026-01-01")
	hotel := mustTx(t, s, TransactionInput{AccountID: a, Date: "2026-07-10", Label: "Hôtel", Amount: -300_00, Tags: []string{"vacances 2027", "déductible"}})
	train := mustTx(t, s, TransactionInput{AccountID: a, Date: "2026-07-11", Label: "Train", Amount: -80_00, Tags: []string{"Vacances 2027"}})
	mustTx(t, s, TransactionInput{AccountID: a, Date: "2026-07-12", Label: "Courses", Amount: -50_00})
	if _, err := s.CreateTransfer(ctx, TransferInput{FromAccountID: a, ToAccountID: b, Date: "2026-07-12", Label: "Épargne", Amount: 10_00}); err != nil {
		t.Fatal(err)
	}

	if tx, _ := s.Transaction(ctx, hotel); !slices.Equal(tx.Tags, []string{"déductible", "vacances 2027"}) {
		t.Errorf("étiquettes lues : %q", tx.Tags)
	}
	page, _ := s.Search(ctx, Filter{Tag: "VACANCES 2027"}, 0)
	if page.Count != 2 || page.Sum != -380_00 {
		t.Errorf("filtre par étiquette : %d, %d", page.Count, page.Sum)
	}

	// Par lot, sur toute une recherche : les virements sont laissés de côté.
	res, err := s.BatchUpdate(ctx, Selection{Filter: &Filter{From: "2026-07-11"}}, BatchChange{AddTag: "juillet"})
	if err != nil || res.Updated != 2 || res.SkippedTransfers != 2 {
		t.Fatalf("ajout par lot : %+v, %v", res, err)
	}
	if _, err := s.BatchUpdate(ctx, Selection{IDs: []int64{hotel, train}}, BatchChange{RemoveTag: "vacances 2027"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BatchUpdate(ctx, Selection{IDs: []int64{hotel}}, BatchChange{AddTag: "a, b"}); err == nil {
		t.Error("une seule étiquette par lot")
	}
	tags, _ := s.Tags(ctx)
	if len(tags) != 2 || tags[0] != (Tag{tags[0].ID, "déductible", 1}) || tags[1].Name != "juillet" || tags[1].Count != 2 {
		t.Errorf("étiquettes après le lot : %+v", tags)
	}

	if err := s.RenameTag(ctx, tags[1].ID, "Été 2026"); err != nil {
		t.Fatal(err)
	}
	if err := s.RenameTag(ctx, tags[1].ID, "déductible"); err == nil {
		t.Error("renommage en doublon accepté")
	}
	if err := s.DeleteTag(ctx, tags[0].ID); err != nil {
		t.Fatal(err)
	}
	// Une modification sans étiquette les retire ; l'étiquette inutilisée disparaît.
	if err := s.UpdateTransaction(ctx, train, TransactionInput{AccountID: a, Date: "2026-07-11", Label: "Train", Amount: -80_00}); err != nil {
		t.Fatal(err)
	}
	if tags, _ = s.Tags(ctx); len(tags) != 1 || tags[0].Name != "Été 2026" || tags[0].Count != 1 {
		t.Errorf("étiquettes restantes : %+v", tags)
	}
}

func TestQuickEntry(t *testing.T) {
	s := newTestService(t)
	ctx := t.Context()
	a := mustAccount(t, s, "Courant", 100_00, "2026-01-01")
	if _, ok, err := s.LastEntered(ctx); ok || err != nil {
		t.Errorf("aucune saisie : %v, %v", ok, err)
	}
	bread := mustTx(t, s, TransactionInput{AccountID: a, Date: "2026-10-30", Label: "Boulangerie", Amount: -1_20, CategoryID: categoryID(t, s, "Alimentation › Courses")})
	id, err := s.SaveAsTemplate(ctx, bread, " Pain ")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveAsTemplate(ctx, bread, "pain"); err == nil {
		t.Error("nom de modèle en double accepté")
	}
	created, err := s.UseTemplate(ctx, id, "2026-10-31")
	if err != nil {
		t.Fatal(err)
	}
	tx, _ := s.Transaction(ctx, created)
	if tx.Label != "Boulangerie" || tx.Amount != -1_20 || tx.Date != "2026-10-31" || tx.CategoryName != "Alimentation › Courses" {
		t.Errorf("opération du modèle : %+v", tx)
	}
	if last, ok, _ := s.LastEntered(ctx); !ok || last.ID != created {
		t.Errorf("dernière saisie : %+v", last)
	}
	templates, _ := s.Templates(ctx)
	if len(templates) != 1 || templates[0].Name != "Pain" || templates[0].AccountName != "Courant" {
		t.Errorf("modèles : %+v", templates)
	}
	if err := s.DeleteTemplate(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UseTemplate(ctx, id, "2026-10-31"); err == nil {
		t.Error("modèle supprimé")
	}

	// Pointage unitaire et solde pointé du rapprochement.
	if err := s.SetBankBalance(ctx, a, 98_80, "2026-10-31"); err != nil {
		t.Fatal(err)
	}
	r, _ := s.Reconciliation(ctx, a)
	if r.Cleared != 100_00 || r.ClearedGap() != -1_20 || r.Computed != 97_60 {
		t.Errorf("avant pointage : %+v", r)
	}
	if err := s.SetCleared(ctx, bread, true); err != nil {
		t.Fatal(err)
	}
	if r, _ = s.Reconciliation(ctx, a); r.ClearedGap() != 0 || r.Uncleared != 1 {
		t.Errorf("après pointage : %+v", r)
	}
	if err := s.SetCleared(ctx, 999, true); err == nil {
		t.Error("opération inconnue")
	}
}
