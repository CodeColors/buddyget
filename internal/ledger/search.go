package ledger

import (
	"context"
	"strings"

	"github.com/codecolors/buddyget/internal/platform/calendar"
)

// PageSize est le nombre d'opérations par page de résultats.
const PageSize = 50

// Uncategorized, comme CategoryID d'un filtre, sélectionne les opérations
// sans catégorie (hors virements).
const Uncategorized int64 = -1

// Sort est l'ordre des résultats ; la valeur vide trie par date, des plus
// récentes aux plus anciennes.
type Sort string

const (
	SortDateAsc    Sort = "date"
	SortAmountAsc  Sort = "montant"      // plus fortes dépenses d'abord
	SortAmountDesc Sort = "montant-desc" // plus fortes recettes d'abord
)

// Cleared filtre sur le pointage ; la valeur vide ne filtre pas.
type Cleared string

const (
	ClearedYes Cleared = "oui"
	ClearedNo  Cleared = "non"
)

// Filter décrit une recherche d'opérations. C'est l'unique mécanisme de
// sélection : la liste l'affiche, l'édition par lot s'y applique.
type Filter struct {
	Query      string // mots du libellé ou de la note (préfixes, sans accents)
	AccountID  int64
	CategoryID int64 // catégorie et ses sous-catégories, ou Uncategorized
	From, To   calendar.Date
	// MinAmount et MaxAmount bornent la valeur absolue ; 0 = sans borne.
	MinAmount, MaxAmount int64
	Cleared              Cleared
	Tag                  string // nom d'une étiquette (casse ignorée)
	Sort                 Sort
}

// Page est une page de résultats avec le total de la recherche entière.
type Page struct {
	Items  []Transaction
	Count  int
	Sum    int64
	Offset int
}

// HasPrev indique qu'une page précédente existe.
func (p Page) HasPrev() bool { return p.Offset > 0 }

// HasNext indique qu'une page suivante existe.
func (p Page) HasNext() bool { return p.Offset+len(p.Items) < p.Count }

// Search renvoie une page de résultats, ainsi que leur nombre et leur somme.
func (s *Service) Search(ctx context.Context, f Filter, offset int) (Page, error) {
	where, args := f.where()
	p := Page{Offset: max(offset, 0)}
	if err := s.db.R.QueryRowContext(ctx,
		`SELECT count(*), coalesce(sum(t.amount), 0) FROM transactions t`+where, args...,
	).Scan(&p.Count, &p.Sum); err != nil {
		return Page{}, err
	}

	rows, err := s.db.R.QueryContext(ctx,
		transactionSelect+where+` ORDER BY `+f.Sort.orderBy()+` LIMIT ? OFFSET ?`,
		append(args, PageSize, p.Offset)...)
	if err != nil {
		return Page{}, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		t, err := scanTransaction(rows)
		if err != nil {
			return Page{}, err
		}
		p.Items = append(p.Items, t)
	}
	return p, rows.Err()
}

func (o Sort) orderBy() string {
	switch o {
	case SortDateAsc:
		return "t.date, t.id"
	case SortAmountAsc:
		return "t.amount, t.date DESC, t.id DESC"
	case SortAmountDesc:
		return "t.amount DESC, t.date DESC, t.id DESC"
	}
	return "t.date DESC, t.id DESC"
}

// where traduit le filtre en clause SQL portant sur l'alias t seul, pour
// servir aussi bien au comptage qu'à la sélection d'un lot.
func (f Filter) where() (string, []any) {
	var conds []string
	var args []any
	add := func(cond string, a ...any) {
		conds = append(conds, cond)
		args = append(args, a...)
	}
	if q := ftsQuery(f.Query); q != "" {
		add(`t.id IN (SELECT rowid FROM transactions_fts WHERE transactions_fts MATCH ?)`, q)
	}
	if f.AccountID != 0 {
		add(`t.account_id = ?`, f.AccountID)
	}
	switch {
	case f.CategoryID == Uncategorized:
		add(`t.category_id IS NULL AND t.transfer_id IS NULL`)
	case f.CategoryID != 0:
		add(`t.category_id IN (SELECT id FROM categories WHERE id = ? OR parent_id = ?)`, f.CategoryID, f.CategoryID)
	}
	if f.From != "" {
		add(`t.date >= ?`, f.From)
	}
	if f.To != "" {
		add(`t.date <= ?`, f.To)
	}
	if f.MinAmount > 0 {
		add(`abs(t.amount) >= ?`, f.MinAmount)
	}
	if f.MaxAmount > 0 {
		add(`abs(t.amount) <= ?`, f.MaxAmount)
	}
	switch f.Cleared {
	case ClearedYes:
		add(`t.cleared = 1`)
	case ClearedNo:
		add(`t.cleared = 0`)
	}
	if f.Tag != "" {
		add(`t.id IN (SELECT tt.transaction_id FROM transaction_tags tt JOIN tags g ON g.id = tt.tag_id WHERE g.name = ?)`, f.Tag)
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

// ftsQuery transforme une saisie libre en requête FTS5 : chaque mot devient
// un préfixe obligatoire, les caractères spéciaux sont neutralisés.
func ftsQuery(input string) string {
	var terms []string
	for _, w := range strings.Fields(input) {
		if w = strings.ReplaceAll(w, `"`, ""); w != "" {
			terms = append(terms, `"`+w+`"*`)
		}
	}
	return strings.Join(terms, " ")
}
