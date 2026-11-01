package exports

import (
	"archive/zip"
	"cmp"
	"context"
	"fmt"
	"io"
	"maps"
	"path"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/codecolors/buddyget/internal/attachments"
	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/platform/money"
)

// Operations écrit en CSV les opérations du filtre, par date.
func (s *Service) Operations(ctx context.Context, w io.Writer, f ledger.Filter) error {
	c, err := newCSV(w)
	if err != nil {
		return err
	}
	if err := c.row(operationsHeader...); err != nil {
		return err
	}
	f.Sort = ledger.SortDateAsc
	if err := s.ledger.Each(ctx, f, func(t ledger.Transaction) error { return c.row(operationRow(t)...) }); err != nil {
		return err
	}
	return c.flush()
}

// Dossier écrit une archive ZIP pour un comptable ou la déclaration
// d'impôts : les opérations du filtre en CSV, une synthèse PDF (totaux par
// catégorie et par mois) et, avec files, les pièces jointes, que le CSV
// nomme.
func (s *Service) Dossier(ctx context.Context, w io.Writer, f ledger.Filter, scope string, files bool, now time.Time) error {
	z := zip.NewWriter(w)
	var attached map[int64][]attachments.Attachment
	if files {
		var err error
		if attached, err = s.attachments.ByTransaction(ctx); err != nil {
			return err
		}
	}
	entry, err := z.CreateHeader(&zip.FileHeader{Name: "operations.csv", Method: zip.Deflate, Modified: now})
	if err != nil {
		return err
	}
	c, err := newCSV(entry)
	if err != nil {
		return err
	}
	header := operationsHeader
	if files {
		header = append(slices.Clone(header), "Pièces jointes")
	}
	if err := c.row(header...); err != nil {
		return err
	}
	sum := summary{categories: map[string]*total{}, months: map[string]*total{}}
	var toCopy []attachedFile
	f.Sort = ledger.SortDateAsc
	err = s.ledger.Each(ctx, f, func(t ledger.Transaction) error {
		sum.add(t)
		row := operationRow(t)
		if files {
			var names []string
			for _, a := range attached[t.ID] {
				name := fileName(t, a)
				names = append(names, name)
				toCopy = append(toCopy, attachedFile{a, name})
			}
			row = append(row, strings.Join(names, ", "))
		}
		return c.row(row...)
	})
	if err != nil {
		return err
	}
	if err := c.flush(); err != nil {
		return err
	}
	if entry, err = z.CreateHeader(&zip.FileHeader{Name: "synthese.pdf", Method: zip.Store, Modified: now}); err != nil {
		return err
	}
	if err := sum.pdf(scope, now).write(entry); err != nil {
		return err
	}
	for _, f := range toCopy {
		if err := s.copyAttachment(ctx, z, f, now); err != nil {
			return err
		}
	}
	return z.Close()
}

type attachedFile struct {
	attachments.Attachment
	name string
}

// copyAttachment ajoute une pièce jointe à l'archive, sans la recompresser
// (JPEG et PDF le sont déjà).
func (s *Service) copyAttachment(ctx context.Context, z *zip.Writer, f attachedFile, now time.Time) error {
	_, file, err := s.attachments.Open(ctx, f.ID)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	entry, err := z.CreateHeader(&zip.FileHeader{Name: f.name, Method: zip.Store, Modified: now})
	if err != nil {
		return err
	}
	_, err = io.Copy(entry, file)
	return err
}

// fileName nomme une pièce jointe dans l'archive : date, libellé, numéro.
func fileName(t ledger.Transaction, a attachments.Attachment) string {
	label := strings.Join(strings.FieldsFunc(t.Label, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }), "-")
	if len([]rune(label)) > 40 {
		label = string([]rune(label)[:40])
	}
	return path.Join("pieces", fmt.Sprintf("%s_%s_%d%s", t.Date, label, a.ID, a.Ext()))
}

// summary totalise les opérations d'un dossier, hors virements entre
// comptes : par catégorie et par mois (clé AAAA-MM).
type summary struct {
	count, transfers int
	income, expenses int64
	categories       map[string]*total
	months           map[string]*total
}

type total struct{ income, expenses int64 }

func (t *total) add(amount int64) {
	if amount < 0 {
		t.expenses -= amount
	} else {
		t.income += amount
	}
}

func (s *summary) add(t ledger.Transaction) {
	s.count++
	if t.IsTransfer() {
		s.transfers++
		return
	}
	if t.Amount < 0 {
		s.expenses -= t.Amount
	} else {
		s.income += t.Amount
	}
	bucket(s.categories, cmp.Or(t.CategoryName, "Sans catégorie")).add(t.Amount)
	bucket(s.months, string(t.Date)[:7]).add(t.Amount)
}

func bucket(m map[string]*total, key string) *total {
	t, ok := m[key]
	if !ok {
		t = &total{}
		m[key] = t
	}
	return t
}

func (s *summary) pdf(scope string, now time.Time) *document {
	d := newDocument("Dossier d'opérations", scope, now)
	d.figures([][2]string{
		{"Opérations", fmt.Sprintf("%d", s.count)},
		{"Recettes", money.Format(s.income)},
		{"Dépenses", money.Format(s.expenses)},
		{"Solde", money.FormatSigned(s.income - s.expenses)},
	})
	if s.transfers > 0 {
		d.note(fmt.Sprintf("%d virements entre vos comptes figurent dans le CSV mais pas dans les totaux.", s.transfers))
	}
	if len(s.categories) > 0 {
		d.heading("Par catégorie")
		cols := []column{{"Catégorie", 100, "L"}, {"Dépenses", 40, "R"}, {"Recettes", 40, "R"}}
		var rows [][]string
		names := slices.Sorted(maps.Keys(s.categories))
		slices.SortStableFunc(names, func(a, b string) int {
			return cmp.Compare(s.categories[b].expenses, s.categories[a].expenses)
		})
		for _, n := range names {
			t := s.categories[n]
			rows = append(rows, []string{n, money.Format(t.expenses), money.Format(t.income)})
		}
		d.table(cols, rows)
		d.heading("Par mois")
		rows = nil
		for _, m := range slices.Sorted(maps.Keys(s.months)) {
			t := s.months[m]
			rows = append(rows, []string{m[5:] + "/" + m[:4], money.Format(t.expenses), money.Format(t.income)})
		}
		d.table([]column{{"Mois", 100, "L"}, {"Dépenses", 40, "R"}, {"Recettes", 40, "R"}}, rows)
	}
	d.note("Le détail des opérations, et le nom de leurs pièces jointes, est dans operations.csv.")
	return d
}
