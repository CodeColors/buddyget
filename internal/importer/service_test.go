package importer

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/platform/db"
)

type fixture struct {
	t       *testing.T
	s       *Service
	ledger  *ledger.Service
	account int64
	dataDir string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dataDir := t.TempDir()
	d, err := db.Open(t.Context(), filepath.Join(dataDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	l := ledger.NewService(d)
	account, err := l.CreateAccount(t.Context(), ledger.AccountInput{
		Name: "Courant", Kind: ledger.AccountCurrent, Bank: ledger.BankSG, OpeningDate: "2026-01-01",
	})
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{t: t, s: NewService(d, dataDir, 1<<20), ledger: l, account: account, dataDir: dataDir}
}

func (f *fixture) upload(name string, data []byte) Draft {
	f.t.Helper()
	token, err := f.s.Upload(f.t.Context(), f.account, name, bytes.NewReader(data))
	if err != nil {
		f.t.Fatal(err)
	}
	d, err := f.s.Draft(f.t.Context(), token)
	if err != nil {
		f.t.Fatal(err)
	}
	return d
}

func (f *fixture) uploadFile(path string) Draft {
	f.t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		f.t.Fatal(err)
	}
	return f.upload(filepath.Base(path), data)
}

func (f *fixture) commit(d Draft) Import {
	f.t.Helper()
	imp, err := f.s.Commit(f.t.Context(), d.Token)
	if err != nil {
		f.t.Fatal(err)
	}
	return imp
}

func (f *fixture) count() int {
	f.t.Helper()
	p, err := f.ledger.Search(f.t.Context(), ledger.Filter{AccountID: f.account}, 0)
	if err != nil {
		f.t.Fatal(err)
	}
	return p.Count
}

func TestImportOverlappingExports(t *testing.T) {
	f := newFixture(t)
	d := f.uploadFile("../../testdata/sg/releve-octobre.csv")
	if d.Profile != "Société Générale" || d.Format != FormatCSV || d.Counts != (Counts{New: 6, Selected: 6}) {
		t.Fatalf("brouillon : %+v", d)
	}
	imp := f.commit(d)
	if imp.Added != 6 || imp.Read != 6 || f.count() != 6 {
		t.Fatalf("premier import : %+v, %d opérations", imp, f.count())
	}
	if _, err := f.s.Draft(t.Context(), d.Token); !errors.Is(err, ErrNotFound) {
		t.Error("le brouillon doit disparaître après validation")
	}
	if _, err := os.Stat(filepath.Join(f.dataDir, "imports", d.Token)); !errors.Is(err, os.ErrNotExist) {
		t.Error("le fichier déposé doit être supprimé après validation")
	}

	// L'export suivant reprend les deux achats identiques du 31/10.
	d = f.uploadFile("../../testdata/sg/releve-chevauchant.csv")
	if d.Counts != (Counts{New: 2, Duplicate: 2, Selected: 2}) {
		t.Fatalf("export chevauchant : %+v", d.Counts)
	}
	if imp = f.commit(d); imp.Added != 2 || imp.Duplicate != 2 || f.count() != 8 {
		t.Fatalf("second import : %+v, %d opérations", imp, f.count())
	}

	// Réimporter le premier fichier n'ajoute rien.
	d = f.uploadFile("../../testdata/sg/releve-octobre.csv")
	if d.Counts != (Counts{Duplicate: 6}) {
		t.Fatalf("réimport : %+v", d.Counts)
	}
	_, err := f.s.Commit(t.Context(), d.Token)
	expectInputError(t, err)
}

func TestImportSpreadsheets(t *testing.T) {
	f := newFixture(t)
	if d := f.uploadFile("../../testdata/bnp/releve-octobre.xlsx"); d.Profile != "BNP Paribas" || d.Counts.New != 4 {
		t.Errorf("XLSX : %+v", d)
	}
	if d := f.uploadFile("../../testdata/generique/debit-credit.xls"); d.Profile != "Générique" || d.Counts.New != 3 {
		t.Errorf("XLS : %+v", d)
	}
}

func TestImportSelectionAndErrors(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	csv := "Date;Libellé;Montant\n01/10/2026;Loyer;-700\n02/10/2026;Pain;-1,10\n03/10/2026;;-5\nSolde au 31/10;;1 234,56\n"
	d := f.upload("releve.csv", []byte(csv))
	if d.Counts != (Counts{New: 2, Error: 2, Selected: 2}) {
		t.Fatalf("décompte : %+v", d.Counts)
	}
	errs, total, err := f.s.Lines(ctx, d.Token, StatusError, 1)
	if err != nil || total != 2 || errs[0].Error != "Libellé absent." || errs[1].Line != 5 || errs[0].Selected {
		t.Fatalf("lignes en erreur : %+v (%d), %v", errs, total, err)
	}

	// Décocher « Pain » ; une ligne en erreur ne peut pas être cochée.
	if err := f.s.Select(ctx, d.Token, []int{2, 3, 4}, []int{2, 4}); err != nil {
		t.Fatal(err)
	}
	if d, _ = f.s.Draft(ctx, d.Token); d.Counts.Selected != 1 {
		t.Fatalf("après sélection : %+v", d.Counts)
	}
	imp := f.commit(d)
	if imp.Added != 1 || imp.Skipped != 1 || imp.Errors != 2 || imp.Read != 4 {
		t.Errorf("import : %+v", imp)
	}

	d = f.upload("releve.csv", []byte(csv))
	if err := f.s.SelectAll(ctx, d.Token, false); err != nil {
		t.Fatal(err)
	}
	if d, _ = f.s.Draft(ctx, d.Token); d.Counts.Selected != 0 {
		t.Errorf("tout décocher : %+v", d.Counts)
	}
}

func TestManualMappingAndSavedProfile(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	d := f.uploadFile("../../testdata/generique/sans-entete.csv")
	if d.Mapping != nil || d.Counts.Total() != 0 {
		t.Fatalf("sans en-tête, aucun profil ne doit s'appliquer : %+v", d)
	}
	head, err := f.s.Head(ctx, d.Token)
	if err != nil || len(head) != 3 || head[0].Cells[1] != "Loyer octobre" {
		t.Fatalf("aperçu brut : %+v, %v", head, err)
	}
	expectInputError(t, f.s.SetMapping(ctx, d.Token, Mapping{Date: 0, Labels: []int{1}, Amount: NoColumn, Debit: NoColumn, Credit: NoColumn}, ""))
	expectInputError(t, f.s.SetMapping(ctx, d.Token, Mapping{Date: 0, Labels: []int{1}, Amount: 2}, "Sans en-tête"))
	if err := f.s.SetMapping(ctx, d.Token, Mapping{Date: 0, Labels: []int{1}, Amount: 2, Debit: NoColumn, Credit: NoColumn}, ""); err != nil {
		t.Fatal(err)
	}
	if d, _ = f.s.Draft(ctx, d.Token); d.Counts.New != 3 || d.Profile != "Correspondance manuelle" {
		t.Fatalf("après correspondance : %+v", d)
	}

	// Un en-tête inconnu, associé à la main puis enregistré, est reconnu ensuite.
	unknown := []byte("Le;Quoi;Combien\n01/10/2026;Loyer;-700\n")
	d = f.upload("autre.csv", unknown)
	m := Mapping{HeaderLine: 1, Date: 0, Labels: []int{1}, Amount: 2, Debit: NoColumn, Credit: NoColumn}
	if err := f.s.SetMapping(ctx, d.Token, m, "Ma banque"); err != nil {
		t.Fatal(err)
	}
	if d = f.upload("autre.csv", unknown); d.Profile != "Ma banque" || d.Counts.New != 1 {
		t.Errorf("profil enregistré non reconnu : %+v", d)
	}
}

func TestUndoImport(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	imp := f.commit(f.uploadFile("../../testdata/sg/releve-octobre.csv"))
	removed, err := f.s.Undo(ctx, imp.ID)
	if err != nil || removed != 6 || f.count() != 0 {
		t.Fatalf("annulation : %d supprimées, %v, %d restantes", removed, err, f.count())
	}
	_, err = f.s.Undo(ctx, imp.ID)
	expectInputError(t, err)
	if _, err := f.s.Undo(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("import inconnu : %v", err)
	}

	// Après annulation, le même fichier s'importe de nouveau.
	if d := f.uploadFile("../../testdata/sg/releve-octobre.csv"); d.Counts.New != 6 {
		t.Errorf("réimport après annulation : %+v", d.Counts)
	}
	history, err := f.s.History(ctx)
	if err != nil || len(history) != 1 || !history[0].Undone || history[0].AccountName != "Courant" || history[0].Profile != "Société Générale" {
		t.Errorf("historique : %+v, %v", history, err)
	}
}

func TestUploadRefusals(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	f.s.maxBytes = 100
	for name, c := range map[string]struct {
		account int64
		file    string
		data    string
	}{
		"compte inconnu":    {999, "a.csv", "x"},
		"fichier vide":      {f.account, "a.csv", ""},
		"fichier trop gros": {f.account, "a.csv", strings.Repeat("x", 101)},
		"page HTML":         {f.account, "a.xls", "<html></html>"},
		"aucun nom":         {f.account, "", "x"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := f.s.Upload(ctx, c.account, c.file, strings.NewReader(c.data))
			expectInputError(t, err)
		})
	}
	entries, _ := os.ReadDir(filepath.Join(f.dataDir, "imports"))
	if len(entries) != 0 {
		t.Errorf("%d fichiers laissés après des dépôts refusés", len(entries))
	}
	if err := f.ledger.SetAccountArchived(ctx, f.account, true); err != nil {
		t.Fatal(err)
	}
	_, err := f.s.Upload(ctx, f.account, "a.csv", strings.NewReader("x"))
	expectInputError(t, err)
}

func TestAbandonedDraftsArePurged(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	old := f.uploadFile("../../testdata/sg/releve-octobre.csv")
	pending, _ := f.s.Pending(ctx)
	if len(pending) != 1 || pending[0].Filename != "releve-octobre.csv" {
		t.Fatalf("imports en cours : %+v", pending)
	}

	later := time.Now().Add(draftTTL + time.Minute)
	f.s.now = func() time.Time { return later }
	if pending, _ = f.s.Pending(ctx); len(pending) != 0 {
		t.Error("un brouillon expiré ne doit plus être proposé")
	}
	f.uploadFile("../../testdata/generique/sans-entete.csv")
	if _, err := f.s.Draft(ctx, old.Token); !errors.Is(err, ErrNotFound) {
		t.Error("le brouillon expiré doit être purgé au dépôt suivant")
	}
	if _, err := os.Stat(filepath.Join(f.dataDir, "imports", old.Token)); !errors.Is(err, os.ErrNotExist) {
		t.Error("le fichier du brouillon expiré doit être supprimé")
	}
}
