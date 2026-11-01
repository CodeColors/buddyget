package exports

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/codecolors/buddyget/internal/attachments"
	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/platform/db"
	"github.com/codecolors/buddyget/internal/reports"
)

var now = time.Date(2026, 11, 1, 9, 30, 0, 0, time.UTC)

const pdfFile = "%PDF-1.4\n1 0 obj << >> endobj\ntrailer << >>\n%%EOF\n"

type env struct {
	db      *db.DB
	s       *Service
	ledger  *ledger.Service
	reports *reports.Service
	files   *attachments.Service
	account int64
}

func newEnv(t testing.TB) env {
	t.Helper()
	compressPDF = false
	t.Cleanup(func() { compressPDF = true })
	dir := t.TempDir()
	d, err := db.Open(t.Context(), filepath.Join(dir, "exports.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	l := ledger.NewService(d)
	files := attachments.NewService(d, dir, 1<<20, 10<<20)
	account, err := l.CreateAccount(t.Context(), ledger.AccountInput{Name: "Courant", Kind: ledger.AccountCurrent, Bank: ledger.BankSG, OpeningDate: "2026-01-01"})
	if err != nil {
		t.Fatal(err)
	}
	return env{db: d, s: NewService(l, files), ledger: l, reports: reports.NewService(d, l), files: files, account: account}
}

func (e env) op(t *testing.T, date calendar.Date, label string, amount int64, tags ...string) int64 {
	t.Helper()
	id, err := e.ledger.CreateTransaction(t.Context(), ledger.TransactionInput{AccountID: e.account, Date: date, Label: label, Amount: amount, Tags: tags})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func readCSV(t *testing.T, data []byte) [][]string {
	t.Helper()
	if !bytes.HasPrefix(data, []byte("\xef\xbb\xbf")) {
		t.Fatal("CSV sans BOM UTF-8")
	}
	r := csv.NewReader(bytes.NewReader(data[3:]))
	r.Comma = ';'
	rows, err := r.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestOperationsCSV(t *testing.T) {
	e := newEnv(t)
	e.op(t, "2026-10-02", "=HYPERLINK(\"x\")", -12_34)
	e.op(t, "2026-10-01", "Salaire", 2_500_00, "déductible", "travail")
	e.op(t, "2025-12-31", "Avant", -1_00)
	var buf bytes.Buffer
	if err := e.s.Operations(t.Context(), &buf, ledger.Filter{From: "2026-01-01"}); err != nil {
		t.Fatal(err)
	}
	rows := readCSV(t, buf.Bytes())
	want := [][]string{
		operationsHeader,
		{"01/10/2026", "Courant", "Salaire", "2500,00", "", "déductible, travail", "", "non", ""},
		{"02/10/2026", "Courant", "'=HYPERLINK(\"x\")", "-12,34", "", "", "", "non", ""},
	}
	if len(rows) != len(want) {
		t.Fatalf("lignes : %q", rows)
	}
	for i := range want {
		if strings.Join(rows[i], "|") != strings.Join(want[i], "|") {
			t.Errorf("ligne %d : %q, attendu %q", i, rows[i], want[i])
		}
	}
}

func TestDossier(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	garage := e.op(t, "2026-03-05", "Garage / vidange", -250_00, "déductible")
	e.op(t, "2026-04-10", "Bureau", -80_00, "déductible")
	e.op(t, "2026-04-12", "Courses", -60_00)
	if _, err := e.files.Add(ctx, garage, "facture.pdf", strings.NewReader(pdfFile)); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := e.s.Dossier(ctx, &buf, ledger.Filter{Tag: "déductible"}, "Étiquette « déductible », année 2026", true, now); err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	entries := map[string][]byte{}
	for _, f := range z.File {
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		entries[f.Name], _ = io.ReadAll(r)
		_ = r.Close()
	}
	rows := readCSV(t, entries["operations.csv"])
	if len(rows) != 3 || rows[0][len(rows[0])-1] != "Pièces jointes" || rows[1][2] != "Garage / vidange" {
		t.Fatalf("CSV du dossier : %q", rows)
	}
	piece := rows[1][len(rows[1])-1]
	if !strings.HasPrefix(piece, "pieces/2026-03-05_Garage-vidange_") || !strings.HasSuffix(piece, ".pdf") || string(entries[piece]) != pdfFile {
		t.Errorf("pièce jointe %q : %d octets", piece, len(entries[piece]))
	}
	synth := string(entries["synthese.pdf"])
	for _, want := range []string{"%PDF-", "Dossier d'op", "330,00", "Par cat", "03/2026", "04/2026"} {
		if !strings.Contains(synth, want) {
			t.Errorf("synthèse : %q absent", want)
		}
	}
	if len(entries) != 3 {
		t.Errorf("entrées : %d", len(entries))
	}

	// Sans pièces jointes : pas de colonne ni de dossier pieces/.
	buf.Reset()
	if err := e.s.Dossier(ctx, &buf, ledger.Filter{}, "Tout", false, now); err != nil {
		t.Fatal(err)
	}
	z, _ = zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if len(z.File) != 2 {
		t.Errorf("dossier sans pièces : %d entrées", len(z.File))
	}
}

func TestReportExports(t *testing.T) {
	e := newEnv(t)
	e.op(t, "2026-10-01", "Salaire", 2_000_00)
	e.op(t, "2026-10-05", "CB CARREFOUR", -150_00)
	r, err := e.reports.Monthly(t.Context(), "2026-10-15")
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := ReportCSV(&buf, "Rapport d'octobre 2026", r); err != nil {
		t.Fatal(err)
	}
	rows := readCSV(t, buf.Bytes())
	found := map[string]bool{}
	for _, row := range rows {
		found[row[0]+"|"+row[1]+"|"+row[2]] = true
	}
	for _, want := range []string{"Total|Revenus|2000,00", "Total|Dépenses|150,00", "Total|Épargné|1850,00", "Catégorie|Sans catégorie|150,00"} {
		if !found[want] {
			t.Errorf("CSV du rapport : %q absent de %q", want, rows)
		}
	}
	buf.Reset()
	if err := ReportPDF(&buf, "Rapport d'octobre 2026", r, now); err != nil {
		t.Fatal(err)
	}
	pdf := buf.String()
	// Texte en Windows-1252 : « é » est l'octet E9, « € » l'octet 80.
	for _, want := range []string{"%PDF-", "Rapport d'octobre 2026", "D\xe9penses par cat\xe9gorie", "2\xa0000,00\xa0\x80", "Page 1/1", "01/11/2026 09:30"} {
		if !strings.Contains(pdf, want) {
			t.Errorf("PDF du rapport : %q absent", want)
		}
	}
}
