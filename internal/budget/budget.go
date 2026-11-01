// Package budget gère les enveloppes mensuelles par catégorie : plafond,
// report d'un mois sur l'autre, consommé et restant, tendance sur douze
// mois. Une dépense compte dans l'enveloppe de sa catégorie, sinon dans
// celle de sa catégorie parente : jamais dans deux enveloppes.
package budget

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/platform/db"
	"github.com/codecolors/buddyget/internal/platform/money"
	"github.com/codecolors/buddyget/internal/platform/text"
	"github.com/codecolors/buddyget/internal/platform/validation"
)

// Budget est l'enveloppe mensuelle d'une catégorie de dépenses.
type Budget struct {
	CategoryID    int64
	CategoryName  string
	CategoryColor string
	Amount        int64 // plafond mensuel
	CarryOver     bool  // le reste ou le dépassement passe au mois suivant
	Since         calendar.Date
}

// Input est la saisie d'une enveloppe.
type Input struct {
	CategoryID int64
	Amount     int64
	CarryOver  bool
}

// Level est l'état d'une enveloppe.
type Level uint8

const (
	OK   Level = iota // dans le budget
	Risk              // seuil d'alerte atteint
	Over              // dépassée
)

// Label décrit l'état en toutes lettres (la couleur ne fait que souligner).
func (l Level) Label() string {
	switch l {
	case Risk:
		return "Bientôt atteint"
	case Over:
		return "Dépassé"
	}
	return "Dans le budget"
}

// Class renvoie la classe CSS de l'état.
func (l Level) Class() string {
	switch l {
	case Risk:
		return "risk"
	case Over:
		return "over"
	}
	return "ok"
}

// Status est l'état d'une enveloppe sur un mois.
type Status struct {
	Budget
	Carry     int64 // report du mois précédent : reste (positif) ou dépassement
	Available int64 // plafond + report
	Spent     int64 // dépenses nettes du mois (remboursements déduits)
}

// Remaining renvoie ce qui reste à dépenser (négatif en cas de dépassement).
func (s Status) Remaining() int64 { return s.Available - s.Spent }

// Percent renvoie la part consommée, en pourcentage arrondi.
func (s Status) Percent() int {
	if s.Available <= 0 {
		if s.Spent > 0 {
			return 100
		}
		return 0
	}
	return int((max(s.Spent, 0)*100 + s.Available/2) / s.Available)
}

// Level renvoie l'état de l'enveloppe ; risk est le seuil d'alerte en %.
func (s Status) Level(risk int) Level {
	switch {
	case s.Spent > s.Available:
		return Over
	case s.Spent > 0 && s.Percent() >= risk:
		return Risk
	}
	return OK
}

// MonthTotal compare les dépenses d'un mois au total des enveloppes.
type MonthTotal struct {
	Month calendar.Date // premier jour du mois
	Spent int64
}

// Service gère les enveloppes.
type Service struct {
	db     *db.DB
	ledger *ledger.Service
}

// NewService crée le service des budgets.
func NewService(d *db.DB, l *ledger.Service) *Service { return &Service{db: d, ledger: l} }

// Budgets renvoie les enveloppes, par nom de catégorie.
func (s *Service) Budgets(ctx context.Context) ([]Budget, error) {
	cats, err := s.ledger.Categories(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]ledger.Category, len(cats))
	for _, c := range cats {
		byID[c.ID] = c
	}
	rows, err := s.db.R.QueryContext(ctx, `SELECT category_id, amount, carry_over, since FROM budgets`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Budget
	for rows.Next() {
		var b Budget
		if err := rows.Scan(&b.CategoryID, &b.Amount, &b.CarryOver, &b.Since); err != nil {
			return nil, err
		}
		c := byID[b.CategoryID]
		b.CategoryName, b.CategoryColor = c.FullName(), c.Color
		out = append(out, b)
	}
	slices.SortFunc(out, func(a, b Budget) int { return strings.Compare(text.Fold(a.CategoryName), text.Fold(b.CategoryName)) })
	return out, rows.Err()
}

// Save crée ou modifie l'enveloppe d'une catégorie de dépenses ; le report
// part du mois de création.
func (s *Service) Save(ctx context.Context, in Input, today calendar.Date) error {
	if in.Amount <= 0 || in.Amount > money.Max {
		return validation.Error("Indiquez un plafond mensuel positif.")
	}
	var kind ledger.CategoryKind
	err := s.db.R.QueryRowContext(ctx, `SELECT kind FROM categories WHERE id = ?`, in.CategoryID).Scan(&kind)
	if err != nil || kind != ledger.CategoryExpense {
		return validation.Error("Choisissez une catégorie de dépenses.")
	}
	_, err = s.db.W.ExecContext(ctx, `
		INSERT INTO budgets (category_id, amount, carry_over, since) VALUES (?, ?, ?, ?)
		ON CONFLICT (category_id) DO UPDATE SET amount = excluded.amount, carry_over = excluded.carry_over`,
		in.CategoryID, in.Amount, in.CarryOver, monthStart(today))
	return err
}

// Delete supprime l'enveloppe d'une catégorie.
func (s *Service) Delete(ctx context.Context, categoryID int64) error {
	_, err := s.db.W.ExecContext(ctx, `DELETE FROM budgets WHERE category_id = ?`, categoryID)
	return err
}

// Month renvoie l'état des enveloppes pour le mois contenant le jour day.
func (s *Service) Month(ctx context.Context, day calendar.Date) ([]Status, error) {
	budgets, err := s.Budgets(ctx)
	if err != nil || len(budgets) == 0 {
		return nil, err
	}
	month := monthStart(day)
	from := month
	for _, b := range budgets {
		if b.CarryOver && b.Since < from {
			from = b.Since
		}
	}
	spent, err := s.spending(ctx, budgets, from, monthEnd(month))
	if err != nil {
		return nil, err
	}
	out := make([]Status, len(budgets))
	for i, b := range budgets {
		st := Status{Budget: b}
		if b.CarryOver {
			for m := b.Since; m < month; m = nextMonth(m) {
				st.Carry += b.Amount - spent[b.CategoryID][m]
			}
		}
		st.Available, st.Spent = b.Amount+st.Carry, spent[b.CategoryID][month]
		out[i] = st
	}
	return out, nil
}

// History renvoie les dépenses mensuelles des catégories budgétées sur les
// months derniers mois, mois en cours compris.
func (s *Service) History(ctx context.Context, today calendar.Date, months int) ([]MonthTotal, error) {
	budgets, err := s.Budgets(ctx)
	if err != nil || len(budgets) == 0 {
		return nil, err
	}
	current := monthStart(today)
	first := calendar.FromTime(current.Time().AddDate(0, 1-months, 0))
	spent, err := s.spending(ctx, budgets, first, monthEnd(current))
	if err != nil {
		return nil, err
	}
	out := make([]MonthTotal, 0, months)
	for m := first; m <= current; m = nextMonth(m) {
		t := MonthTotal{Month: m}
		for _, b := range budgets {
			t.Spent += spent[b.CategoryID][m]
		}
		out = append(out, t)
	}
	return out, nil
}

// Total renvoie la somme des plafonds mensuels.
func Total(budgets []Budget) int64 {
	var t int64
	for _, b := range budgets {
		t += b.Amount
	}
	return t
}

// spending renvoie les dépenses nettes de chaque enveloppe, par mois (clé :
// premier jour du mois), entre from et to, hors virements internes.
func (s *Service) spending(ctx context.Context, budgets []Budget, from, to calendar.Date) (map[int64]map[calendar.Date]int64, error) {
	out := make(map[int64]map[calendar.Date]int64, len(budgets))
	for _, b := range budgets {
		out[b.CategoryID] = map[calendar.Date]int64{}
	}
	rows, err := s.db.R.QueryContext(ctx, `
		SELECT t.category_id, coalesce(c.parent_id, 0), substr(t.date, 1, 7) || '-01', sum(t.amount)
		FROM transactions t JOIN categories c ON c.id = t.category_id
		WHERE t.transfer_id IS NULL AND t.date >= ? AND t.date <= ?
		GROUP BY 1, 2, 3`, from, to)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var cat, parent, sum int64
		var month calendar.Date
		if err := rows.Scan(&cat, &parent, &month, &sum); err != nil {
			return nil, err
		}
		// La catégorie elle-même, sinon son parent : l'enveloppe la plus
		// précise l'emporte.
		if m, ok := out[cat]; ok {
			m[month] -= sum
		} else if m, ok := out[parent]; ok {
			m[month] -= sum
		}
	}
	return out, rows.Err()
}

func monthStart(d calendar.Date) calendar.Date {
	t := d.Time()
	return calendar.FromTime(time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC))
}

func monthEnd(month calendar.Date) calendar.Date {
	return calendar.FromTime(month.Time().AddDate(0, 1, -1))
}

func nextMonth(month calendar.Date) calendar.Date {
	return calendar.FromTime(month.Time().AddDate(0, 1, 0))
}
