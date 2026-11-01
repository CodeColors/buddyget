// Package reports établit les rapports mensuel et annuel : dépenses par
// catégorie et leur évolution, revenus, taux d'épargne, principaux
// commerçants, totaux par étiquette. Les virements internes et les
// opérations de catégorie « virement » (épargne) n'y sont ni des dépenses
// ni des revenus.
package reports

import (
	"cmp"
	"context"
	"math"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/platform/db"
	"github.com/codecolors/buddyget/internal/platform/text"
)

// topMerchants est le nombre de commerçants du classement.
const topMerchants = 10

// Report est un rapport sur une période.
type Report struct {
	From, To    calendar.Date
	Income      int64 // revenus
	Expenses    int64 // dépenses nettes (remboursements déduits), positives
	Previous    Totals
	Categories  []CategoryLine
	Merchants   []Merchant
	Tags        []TagLine
	Months      []MonthLine // rapport annuel : mois par mois
	HasPrevious bool        // la période précédente a des opérations
}

// Totals résume une période.
type Totals struct {
	Income, Expenses int64
}

// Saved renvoie ce qui reste des revenus après les dépenses.
func (r Report) Saved() int64 { return r.Income - r.Expenses }

// SavingsRate renvoie la part des revenus non dépensée, en pourcentage
// arrondi ; 0 sans revenus (à vérifier avec Income).
func (r Report) SavingsRate() int {
	if r.Income <= 0 {
		return 0
	}
	return int(math.Round(float64(r.Saved()) * 100 / float64(r.Income)))
}

// CategoryLine est le total d'une catégorie de premier niveau (ses
// sous-catégories comprises) sur la période et la période précédente.
type CategoryLine struct {
	Name     string // vide : sans catégorie
	Color    string
	Amount   int64
	Previous int64
	Share    int // part des dépenses, en %
}

// Change renvoie l'évolution par rapport à la période précédente, en %
// arrondi ; 0 sans dépense précédente (à vérifier avec Previous).
func (c CategoryLine) Change() int {
	if c.Previous <= 0 {
		return 0
	}
	return int(math.Round(float64(c.Amount-c.Previous) * 100 / float64(c.Previous)))
}

// Merchant est le total des dépenses chez un commerçant.
type Merchant struct {
	Name   string
	Amount int64
	Count  int
}

// TagLine est le total d'une étiquette : dépenses nettes et revenus.
type TagLine struct {
	Name             string
	Expenses, Income int64
	Count            int
}

// MonthLine résume un mois du rapport annuel.
type MonthLine struct {
	Month            calendar.Date
	Income, Expenses int64
}

// Service établit les rapports.
type Service struct {
	db     *db.DB
	ledger *ledger.Service
}

// NewService crée le service des rapports.
func NewService(d *db.DB, l *ledger.Service) *Service { return &Service{db: d, ledger: l} }

// Monthly établit le rapport du mois contenant day.
func (s *Service) Monthly(ctx context.Context, day calendar.Date) (Report, error) {
	t := day.Time()
	first := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	return s.report(ctx, first, first.AddDate(0, 1, 0), first.AddDate(0, -1, 0), false)
}

// Annual établit le rapport d'une année civile, mois par mois.
func (s *Service) Annual(ctx context.Context, year int) (Report, error) {
	first := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
	return s.report(ctx, first, first.AddDate(1, 0, 0), first.AddDate(-1, 0, 0), true)
}

// op est une opération retenue par un rapport.
type op struct {
	date            calendar.Date
	label           string
	amount          int64
	top             int64
	kind            ledger.CategoryKind
	tags            []string
	inPeriod        bool
	month           calendar.Date
	expense, income bool
}

// report établit le rapport de [from, to[, mois par mois si byMonth, et le
// compare à [previous, from[.
func (s *Service) report(ctx context.Context, from, to, previous time.Time, byMonth bool) (Report, error) {
	r := Report{From: calendar.FromTime(from), To: calendar.FromTime(to.AddDate(0, 0, -1))}
	ops, err := s.load(ctx, calendar.FromTime(previous), r.To, r.From)
	if err != nil {
		return r, err
	}
	cats, err := s.ledger.Categories(ctx)
	if err != nil {
		return r, err
	}
	byID := make(map[int64]ledger.Category, len(cats))
	for _, c := range cats {
		byID[c.ID] = c
	}

	lines := map[int64]*CategoryLine{}
	merchants := map[string]*Merchant{}
	tags := map[string]*TagLine{}
	months := map[calendar.Date]*MonthLine{}
	for _, o := range ops {
		if !o.expense && !o.income {
			continue
		}
		if !o.inPeriod {
			r.HasPrevious = true
			if o.expense {
				r.Previous.Expenses -= o.amount
				line(lines, byID, o.top).Previous -= o.amount
			} else {
				r.Previous.Income += o.amount
			}
			continue
		}
		if byMonth {
			m := months[o.month]
			if m == nil {
				m = &MonthLine{Month: o.month}
				months[o.month] = m
			}
			if o.expense {
				m.Expenses -= o.amount
			} else {
				m.Income += o.amount
			}
		}
		for _, name := range o.tags {
			tl := tags[name]
			if tl == nil {
				tl = &TagLine{Name: name}
				tags[name] = tl
			}
			tl.Count++
			if o.expense {
				tl.Expenses -= o.amount
			} else {
				tl.Income += o.amount
			}
		}
		if o.income {
			r.Income += o.amount
			continue
		}
		r.Expenses -= o.amount
		line(lines, byID, o.top).Amount -= o.amount
		if o.amount < 0 {
			key := merchantKey(o.label)
			m := merchants[key]
			if m == nil { // libellé le plus récent
				m = &Merchant{Name: o.label}
				merchants[key] = m
			}
			m.Amount -= o.amount
			m.Count++
		}
	}
	r.Categories = sortedLines(lines, r.Expenses)
	r.Merchants = sortedMerchants(merchants)
	for _, t := range tags {
		r.Tags = append(r.Tags, *t)
	}
	slices.SortFunc(r.Tags, func(a, b TagLine) int {
		return cmp.Or(cmp.Compare(b.Expenses, a.Expenses), strings.Compare(a.Name, b.Name))
	})
	if byMonth {
		for m := from; m.Before(to); m = m.AddDate(0, 1, 0) {
			line := MonthLine{Month: calendar.FromTime(m)}
			if ml := months[line.Month]; ml != nil {
				line = *ml
			}
			r.Months = append(r.Months, line)
		}
	}
	return r, nil
}

// load lit les opérations (hors virements internes) de first à last ; celles
// à partir de start sont dans la période du rapport.
func (s *Service) load(ctx context.Context, first, last, start calendar.Date) ([]op, error) {
	rows, err := s.db.R.QueryContext(ctx, `
		SELECT t.date, t.label, t.amount, coalesce(c.parent_id, c.id, 0), coalesce(c.kind, ''),
			coalesce((SELECT group_concat(g.name, char(31)) FROM transaction_tags tt JOIN tags g ON g.id = tt.tag_id
				WHERE tt.transaction_id = t.id), '')
		FROM transactions t LEFT JOIN categories c ON c.id = t.category_id
		WHERE t.transfer_id IS NULL AND t.date >= ? AND t.date <= ?
		ORDER BY t.date DESC, t.id DESC`, first, last)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []op
	for rows.Next() {
		var o op
		var tags string
		if err := rows.Scan(&o.date, &o.label, &o.amount, &o.top, &o.kind, &tags); err != nil {
			return nil, err
		}
		if tags != "" {
			o.tags = strings.Split(tags, "\x1f")
		}
		o.inPeriod = o.date >= start
		o.month = calendar.Date(string(o.date)[:8] + "01")
		// La catégorie décide ; sans catégorie, le signe. Les catégories de
		// virement (épargne) ne comptent ni en dépense ni en revenu.
		switch o.kind {
		case ledger.CategoryExpense:
			o.expense = true
		case ledger.CategoryIncome:
			o.income = true
		case "":
			o.expense, o.income = o.amount < 0, o.amount > 0
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func line(lines map[int64]*CategoryLine, byID map[int64]ledger.Category, top int64) *CategoryLine {
	l := lines[top]
	if l == nil {
		l = &CategoryLine{}
		if c, ok := byID[top]; ok {
			l.Name, l.Color = c.Name, c.Color
		}
		lines[top] = l
	}
	return l
}

// sortedLines classe les catégories de la plus forte dépense à la plus
// faible et calcule leur part ; celles sans dépense sur la période sont
// gardées si elles en avaient la période précédente.
func sortedLines(lines map[int64]*CategoryLine, total int64) []CategoryLine {
	var out []CategoryLine
	for _, l := range lines {
		if l.Amount == 0 && l.Previous == 0 {
			continue
		}
		if total > 0 {
			l.Share = int(math.Round(float64(max(l.Amount, 0)) * 100 / float64(total)))
		}
		out = append(out, *l)
	}
	slices.SortFunc(out, func(a, b CategoryLine) int {
		return cmp.Or(cmp.Compare(b.Amount, a.Amount), strings.Compare(a.Name, b.Name))
	})
	return out
}

func sortedMerchants(merchants map[string]*Merchant) []Merchant {
	out := make([]Merchant, 0, len(merchants))
	for _, m := range merchants {
		out = append(out, *m)
	}
	slices.SortFunc(out, func(a, b Merchant) int {
		return cmp.Or(cmp.Compare(b.Amount, a.Amount), strings.Compare(a.Name, b.Name))
	})
	return out[:min(len(out), topMerchants)]
}

// merchantKey regroupe les libellés d'un même commerçant : casse et accents
// ignorés, sans les mots contenant un chiffre (dates, références) ni les
// mentions bancaires usuelles.
func merchantKey(label string) string {
	var words []string
	for _, w := range strings.FieldsFunc(text.Fold(label), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if !strings.ContainsFunc(w, unicode.IsDigit) && !bankWords[w] {
			words = append(words, w)
		}
	}
	if len(words) == 0 {
		return text.Fold(label)
	}
	return strings.Join(words, " ")
}

// bankWords sont les mentions ajoutées par les banques aux libellés.
var bankWords = map[string]bool{"cb": true, "carte": true, "prlv": true, "sepa": true, "paiement": true, "achat": true, "vir": true, "fact": true}
