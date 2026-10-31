package ledger

import (
	"testing"
)

func TestBatchUpdateByIDs(t *testing.T) {
	s, courant, livret := searchFixture(t)
	ctx := t.Context()
	divers := categoryID(t, s, "Divers")
	all, _ := s.Search(ctx, Filter{}, 0)
	var ids []int64
	for _, tx := range all.Items {
		ids = append(ids, tx.ID)
	}

	res, err := s.BatchUpdate(ctx, Selection{IDs: ids}, BatchChange{SetCategory: true, CategoryID: divers})
	if err != nil {
		t.Fatal(err)
	}
	if res.Updated != 5 || res.SkippedTransfers != 2 {
		t.Errorf("résultat %+v, attendu 5 modifiées et 2 virements ignorés", res)
	}
	p, _ := s.Search(ctx, Filter{CategoryID: divers}, 0)
	if p.Count != 5 {
		t.Errorf("%d opérations dans Divers, attendu 5", p.Count)
	}

	// Retrait de catégorie et changement de compte sur une sélection.
	res, err = s.BatchUpdate(ctx, Selection{IDs: ids[2:4]}, BatchChange{SetCategory: true, AccountID: livret})
	if err != nil || res.Updated != 2 {
		t.Fatalf("second lot : %+v, %v", res, err)
	}
	p, _ = s.Search(ctx, Filter{AccountID: courant, CategoryID: Uncategorized}, 0)
	if p.Count != 0 {
		t.Errorf("les opérations déplacées ne doivent plus être sur le compte courant : %v", labels(p))
	}
}

func TestBatchUpdateByFilter(t *testing.T) {
	s, _, livret := searchFixture(t)
	ctx := t.Context()
	f := Filter{Query: "cafe"}
	res, err := s.BatchUpdate(ctx, Selection{Filter: &f}, BatchChange{AccountID: livret})
	if err != nil || res.Updated != 2 {
		t.Fatalf("lot par recherche : %+v, %v", res, err)
	}
	p, _ := s.Search(ctx, Filter{Query: "cafe", AccountID: livret}, 0)
	if p.Count != 2 {
		t.Errorf("%d résultats déplacés, attendu 2", p.Count)
	}
}

func TestBatchUpdateValidation(t *testing.T) {
	s, _, _ := searchFixture(t)
	ctx := t.Context()
	_, err := s.BatchUpdate(ctx, Selection{IDs: []int64{1}}, BatchChange{})
	expectInputError(t, err, "modification")
	_, err = s.BatchUpdate(ctx, Selection{}, BatchChange{SetCategory: true})
	expectInputError(t, err, "Sélectionnez")
	_, err = s.BatchUpdate(ctx, Selection{IDs: []int64{1}}, BatchChange{AccountID: 999})
	expectInputError(t, err, "compte")
	_, err = s.BatchUpdate(ctx, Selection{IDs: []int64{1}}, BatchChange{SetCategory: true, CategoryID: 999})
	expectInputError(t, err, "Catégorie")
}
