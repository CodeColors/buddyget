package ledger

import (
	"context"
	"database/sql"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/codecolors/buddyget/internal/platform/db"
	"github.com/codecolors/buddyget/internal/platform/text"
	"github.com/codecolors/buddyget/internal/platform/validation"
)

const tagMaxLen = 40

// tagSeparator sépare les étiquettes lues d'une seule colonne SQL.
const tagSeparator = "\x1f"

// ParseTags lit une saisie « vacances 2027, déductible » : étiquettes
// nettoyées, sans doublon (casse et accents ignorés), dans l'ordre saisi.
func ParseTags(s string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, raw := range strings.Split(s, ",") {
		name := strings.Join(strings.Fields(raw), " ")
		switch key := text.Fold(name); {
		case name == "" || seen[key]:
			continue
		case utf8.RuneCountInString(name) > tagMaxLen:
			return nil, validation.Errorf("Une étiquette ne doit pas dépasser %d caractères.", tagMaxLen)
		default:
			seen[key] = true
			out = append(out, name)
		}
	}
	return out, nil
}

// Tag est une étiquette et le nombre d'opérations qui la portent.
type Tag struct {
	ID    int64
	Name  string
	Count int
}

// Tags renvoie les étiquettes utilisées, par ordre alphabétique.
func (s *Service) Tags(ctx context.Context) ([]Tag, error) {
	rows, err := s.db.R.QueryContext(ctx, `
		SELECT g.id, g.name, count(*) FROM tags g JOIN transaction_tags tt ON tt.tag_id = g.id GROUP BY g.id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Tag
	for rows.Next() {
		var t Tag
		if err := rows.Scan(&t.ID, &t.Name, &t.Count); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	slices.SortFunc(out, func(a, b Tag) int { return strings.Compare(text.Fold(a.Name), text.Fold(b.Name)) })
	return out, rows.Err()
}

// RenameTag renomme une étiquette sur toutes ses opérations.
func (s *Service) RenameTag(ctx context.Context, id int64, name string) error {
	names, err := ParseTags(name)
	if err != nil {
		return err
	}
	if len(names) != 1 {
		return validation.Error("Indiquez un seul nom, sans virgule.")
	}
	res, err := s.db.W.ExecContext(ctx, `UPDATE tags SET name = ? WHERE id = ?`, names[0], id)
	if db.IsUniqueViolation(err) {
		return validation.Error("Une étiquette porte déjà ce nom.")
	}
	return affectedOne(res, err)
}

// DeleteTag retire une étiquette de toutes ses opérations.
func (s *Service) DeleteTag(ctx context.Context, id int64) error {
	res, err := s.db.W.ExecContext(ctx, `DELETE FROM tags WHERE id = ?`, id)
	return affectedOne(res, err)
}

// tagID renvoie l'identifiant d'une étiquette, créée au besoin.
func tagID(ctx context.Context, tx *sql.Tx, name string) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `
		INSERT INTO tags (name) VALUES (?) ON CONFLICT (name) DO UPDATE SET name = name RETURNING id`, name).Scan(&id)
	return id, err
}

// setTags remplace les étiquettes d'une opération.
func setTags(ctx context.Context, tx *sql.Tx, transactionID int64, names []string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM transaction_tags WHERE transaction_id = ?`, transactionID); err != nil {
		return err
	}
	for _, name := range names {
		id, err := tagID(ctx, tx, name)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO transaction_tags (transaction_id, tag_id) VALUES (?, ?)`, transactionID, id); err != nil {
			return err
		}
	}
	return purgeTags(ctx, tx)
}

// purgeTags supprime les étiquettes qui ne servent plus.
func purgeTags(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM tags WHERE id NOT IN (SELECT tag_id FROM transaction_tags)`)
	return err
}

// splitTags lit la colonne des étiquettes d'une opération.
func splitTags(s string) []string {
	if s == "" {
		return nil
	}
	tags := strings.Split(s, tagSeparator)
	slices.SortFunc(tags, func(a, b string) int { return strings.Compare(text.Fold(a), text.Fold(b)) })
	return tags
}
