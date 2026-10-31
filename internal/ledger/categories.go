package ledger

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"regexp"
	"slices"
	"strings"

	"github.com/codecolors/buddyget/internal/platform/db"
	"github.com/codecolors/buddyget/internal/platform/validation"
)

const (
	categoryNameMaxLen = 60
	DefaultColor       = "#757575" // couleur d'une catégorie sans couleur choisie
)

var colorPattern = regexp.MustCompile(`^#[0-9a-f]{6}$`)

// CategoryKind distingue dépenses, revenus et virements.
type CategoryKind string

const (
	CategoryExpense  CategoryKind = "depense"
	CategoryIncome   CategoryKind = "revenu"
	CategoryTransfer CategoryKind = "virement"
)

// CategoryKinds liste les types dans l'ordre d'affichage.
var CategoryKinds = []CategoryKind{CategoryExpense, CategoryIncome, CategoryTransfer}

// Label renvoie le libellé d'un groupe de catégories.
func (k CategoryKind) Label() string {
	switch k {
	case CategoryExpense:
		return "Dépenses"
	case CategoryIncome:
		return "Revenus"
	}
	return "Virements"
}

// Category est une catégorie de premier niveau (ParentID nul) ou une
// sous-catégorie.
type Category struct {
	ID         int64
	ParentID   int64
	ParentName string
	Name       string
	Kind       CategoryKind
	Color      string
}

// FullName renvoie « Parent › Enfant » pour une sous-catégorie.
func (c Category) FullName() string {
	if c.ParentName == "" {
		return c.Name
	}
	return c.ParentName + " › " + c.Name
}

// depth vaut 0 pour une catégorie de premier niveau, 1 sinon.
func (c Category) depth() int {
	if c.ParentID == 0 {
		return 0
	}
	return 1
}

// CategoryInput est la saisie d'une catégorie ; Kind est ignoré pour une
// sous-catégorie, qui prend celui de son parent.
type CategoryInput struct {
	ParentID int64
	Name     string
	Kind     CategoryKind
	Color    string
}

// KindGroup regroupe les catégories d'un type, chaque parent suivi de ses
// sous-catégories.
type KindGroup struct {
	Kind       CategoryKind
	Categories []Category
}

// Categories renvoie toutes les catégories, groupées par type puis par
// parent, dans l'ordre alphabétique français.
func (s *Service) Categories(ctx context.Context) ([]Category, error) {
	rows, err := s.db.R.QueryContext(ctx, `
		SELECT c.id, coalesce(c.parent_id, 0), coalesce(p.name, ''), c.name, c.kind, c.color
		FROM categories c LEFT JOIN categories p ON p.id = c.parent_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Category
	for rows.Next() {
		var c Category
		if err := rows.Scan(&c.ID, &c.ParentID, &c.ParentName, &c.Name, &c.Kind, &c.Color); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b Category) int {
		top := func(c Category) string {
			if c.ParentID == 0 {
				return sortKey(c.Name)
			}
			return sortKey(c.ParentName)
		}
		return cmp.Or(
			cmp.Compare(slices.Index(CategoryKinds, a.Kind), slices.Index(CategoryKinds, b.Kind)),
			strings.Compare(top(a), top(b)),
			cmp.Compare(a.depth(), b.depth()),
			strings.Compare(sortKey(a.Name), sortKey(b.Name)),
		)
	})
	return out, rows.Err()
}

// GroupByKind découpe une liste triée par Categories en groupes par type.
func GroupByKind(cats []Category) []KindGroup {
	var groups []KindGroup
	for _, c := range cats {
		if len(groups) == 0 || groups[len(groups)-1].Kind != c.Kind {
			groups = append(groups, KindGroup{Kind: c.Kind})
		}
		g := &groups[len(groups)-1]
		g.Categories = append(g.Categories, c)
	}
	return groups
}

// Category renvoie une catégorie.
func (s *Service) Category(ctx context.Context, id int64) (Category, error) {
	return category(ctx, s.db.R, id)
}

func category(ctx context.Context, q querier, id int64) (Category, error) {
	var c Category
	err := q.QueryRowContext(ctx, `
		SELECT c.id, coalesce(c.parent_id, 0), coalesce(p.name, ''), c.name, c.kind, c.color
		FROM categories c LEFT JOIN categories p ON p.id = c.parent_id
		WHERE c.id = ?`, id,
	).Scan(&c.ID, &c.ParentID, &c.ParentName, &c.Name, &c.Kind, &c.Color)
	if errors.Is(err, sql.ErrNoRows) {
		return Category{}, ErrNotFound
	}
	return c, err
}

// CreateCategory crée une catégorie et renvoie son identifiant.
func (s *Service) CreateCategory(ctx context.Context, in CategoryInput) (int64, error) {
	var id int64
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		if in, err = validateCategory(ctx, tx, 0, in); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx,
			`INSERT INTO categories (parent_id, name, kind, color) VALUES (nullif(?, 0), ?, ?, ?)`,
			in.ParentID, in.Name, in.Kind, in.Color)
		if err != nil {
			return categoryWriteError(err)
		}
		id, err = res.LastInsertId()
		return err
	})
	return id, err
}

// UpdateCategory modifie une catégorie ; le type d'une catégorie de premier
// niveau est reporté sur ses sous-catégories.
func (s *Service) UpdateCategory(ctx context.Context, id int64, in CategoryInput) error {
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := category(ctx, tx, id); err != nil {
			return err
		}
		in, err := validateCategory(ctx, tx, id, in)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE categories SET parent_id = nullif(?, 0), name = ?, kind = ?, color = ? WHERE id = ?`,
			in.ParentID, in.Name, in.Kind, in.Color, id); err != nil {
			return categoryWriteError(err)
		}
		_, err = tx.ExecContext(ctx, `UPDATE categories SET kind = ? WHERE parent_id = ?`, in.Kind, id)
		return err
	})
}

// DeleteCategory supprime une catégorie et ses sous-catégories ; leurs
// opérations deviennent non catégorisées.
func (s *Service) DeleteCategory(ctx context.Context, id int64) error {
	res, err := s.db.W.ExecContext(ctx, `DELETE FROM categories WHERE id = ?`, id)
	return affectedOne(res, err)
}

// validateCategory contrôle la saisie ; id vaut 0 pour une création.
func validateCategory(ctx context.Context, tx *sql.Tx, id int64, in CategoryInput) (CategoryInput, error) {
	var err error
	if in.Name, err = cleanText(in.Name, "le nom de la catégorie", categoryNameMaxLen, true); err != nil {
		return in, err
	}
	if in.Color = strings.ToLower(strings.TrimSpace(in.Color)); in.Color == "" {
		in.Color = DefaultColor
	} else if !colorPattern.MatchString(in.Color) {
		return in, validation.Error("Couleur invalide.")
	}

	if in.ParentID == 0 {
		if !slices.Contains(CategoryKinds, in.Kind) {
			return in, validation.Error("Choisissez le type de catégorie.")
		}
		return in, nil
	}
	if in.ParentID == id {
		return in, validation.Error("Une catégorie ne peut pas être sa propre parente.")
	}
	parent, err := category(ctx, tx, in.ParentID)
	if errors.Is(err, ErrNotFound) {
		return in, validation.Error("Catégorie parente introuvable.")
	}
	if err != nil {
		return in, err
	}
	if parent.ParentID != 0 {
		return in, validation.Error("Une sous-catégorie ne peut pas contenir d'autres catégories.")
	}
	if id != 0 {
		hasChildren, err := exists(ctx, tx, `SELECT EXISTS (SELECT 1 FROM categories WHERE parent_id = ?)`, id)
		if err != nil {
			return in, err
		}
		if hasChildren {
			return in, validation.Error("Cette catégorie a des sous-catégories : elle ne peut pas devenir une sous-catégorie.")
		}
	}
	in.Kind = parent.Kind
	return in, nil
}

func categoryWriteError(err error) error {
	if db.IsUniqueViolation(err) {
		return validation.Error("Une catégorie porte déjà ce nom à cet endroit.")
	}
	return err
}
