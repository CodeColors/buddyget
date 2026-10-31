// Package categorize porte les règles de catégorisation. C'est l'unique
// mécanisme de catégorisation automatique : il suggère la catégorie des
// lignes importées et s'applique, à la demande, à l'historique.
package categorize

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/codecolors/buddyget/internal/platform/db"
	"github.com/codecolors/buddyget/internal/platform/text"
	"github.com/codecolors/buddyget/internal/platform/validation"
)

const (
	patternMaxLen = 100
	regexMaxLen   = 200
)

// ErrNotFound signale une règle inexistante.
var ErrNotFound = errors.New("règle introuvable")

// MatchKind est la manière de comparer le motif au libellé.
type MatchKind string

const (
	// MatchContains : le libellé contient le texte, casse et accents ignorés.
	MatchContains MatchKind = "contient"
	// MatchRegex : le libellé vérifie l'expression régulière (RE2, casse
	// ignorée), appliquée au libellé tel qu'importé.
	MatchRegex MatchKind = "regex"
)

// MatchKinds liste les manières de comparer dans l'ordre d'affichage.
var MatchKinds = []MatchKind{MatchContains, MatchRegex}

// Label renvoie le libellé affiché.
func (k MatchKind) Label() string {
	if k == MatchRegex {
		return "vérifie l'expression régulière"
	}
	return "contient"
}

// Rule est une règle, avec le nom complet de sa catégorie.
type Rule struct {
	ID            int64
	Pattern       string
	Match         MatchKind
	CategoryID    int64
	CategoryName  string
	CategoryColor string
	Priority      int
}

// RuleInput est la saisie d'une règle.
type RuleInput struct {
	Pattern    string
	Match      MatchKind
	CategoryID int64
	Priority   int
}

// Service gère les règles.
type Service struct {
	db *db.DB
}

// NewService crée le service des règles.
func NewService(d *db.DB) *Service { return &Service{db: d} }

// ruleSelect lit les règles dans leur ordre d'évaluation : priorité
// décroissante, puis motif le plus long (le plus précis), puis le plus ancien.
const ruleSelect = `
	SELECT r.id, r.pattern, r.match, r.category_id,
		CASE WHEN p.id IS NULL THEN c.name ELSE p.name || ' › ' || c.name END, c.color, r.priority
	FROM category_rules r
	JOIN categories c ON c.id = r.category_id
	LEFT JOIN categories p ON p.id = c.parent_id`

const ruleOrder = ` ORDER BY r.priority DESC, length(r.pattern) DESC, r.id`

func scanRule(row interface{ Scan(...any) error }) (Rule, error) {
	var r Rule
	err := row.Scan(&r.ID, &r.Pattern, &r.Match, &r.CategoryID, &r.CategoryName, &r.CategoryColor, &r.Priority)
	return r, err
}

// Rules renvoie toutes les règles dans leur ordre d'évaluation.
func (s *Service) Rules(ctx context.Context) ([]Rule, error) {
	rows, err := s.db.R.QueryContext(ctx, ruleSelect+ruleOrder)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Rule
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Rule renvoie une règle.
func (s *Service) Rule(ctx context.Context, id int64) (Rule, error) {
	r, err := scanRule(s.db.R.QueryRowContext(ctx, ruleSelect+` WHERE r.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Rule{}, ErrNotFound
	}
	return r, err
}

// CreateRule crée une règle et renvoie son identifiant.
func (s *Service) CreateRule(ctx context.Context, in RuleInput) (int64, error) {
	in, err := s.validate(ctx, in)
	if err != nil {
		return 0, err
	}
	res, err := s.db.W.ExecContext(ctx,
		`INSERT INTO category_rules (pattern, match, category_id, priority) VALUES (?, ?, ?, ?)`,
		in.Pattern, in.Match, in.CategoryID, in.Priority)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateRule modifie une règle.
func (s *Service) UpdateRule(ctx context.Context, id int64, in RuleInput) error {
	in, err := s.validate(ctx, in)
	if err != nil {
		return err
	}
	res, err := s.db.W.ExecContext(ctx,
		`UPDATE category_rules SET pattern = ?, match = ?, category_id = ?, priority = ? WHERE id = ?`,
		in.Pattern, in.Match, in.CategoryID, in.Priority, id)
	return affectedOne(res, err)
}

// DeleteRule supprime une règle ; les opérations déjà catégorisées par elle
// le restent.
func (s *Service) DeleteRule(ctx context.Context, id int64) error {
	res, err := s.db.W.ExecContext(ctx, `DELETE FROM category_rules WHERE id = ?`, id)
	return affectedOne(res, err)
}

func affectedOne(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return errors.Join(err, ErrNotFound)
	}
	return nil
}

func (s *Service) validate(ctx context.Context, in RuleInput) (RuleInput, error) {
	in.Pattern = strings.TrimSpace(in.Pattern)
	if in.Pattern == "" {
		return in, validation.Error("Indiquez le texte à rechercher dans les libellés.")
	}
	if !slices.Contains(MatchKinds, in.Match) {
		return in, validation.Error("Choisissez la manière de comparer.")
	}
	maxLen := patternMaxLen
	if in.Match == MatchRegex {
		maxLen = regexMaxLen
	}
	if utf8.RuneCountInString(in.Pattern) > maxLen {
		return in, validation.Errorf("Le motif ne doit pas dépasser %d caractères.", maxLen)
	}
	if _, err := compile(Rule{Pattern: in.Pattern, Match: in.Match}); err != nil {
		return in, validation.Error("Expression régulière invalide.")
	}
	if in.Priority < -100 || in.Priority > 100 {
		return in, validation.Error("La priorité va de -100 à 100.")
	}
	var ok bool
	if err := s.db.R.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM categories WHERE id = ?)`, in.CategoryID).Scan(&ok); err != nil {
		return in, err
	}
	if !ok {
		return in, validation.Error("Choisissez une catégorie.")
	}
	return in, nil
}

// Matcher associe un libellé à la catégorie de la première règle qui
// s'applique.
type Matcher struct {
	rules []compiled
}

type compiled struct {
	category int64
	contains string // forme normalisée du motif « contient »
	re       *regexp.Regexp
}

func compile(r Rule) (compiled, error) {
	c := compiled{category: r.CategoryID}
	if r.Match == MatchRegex {
		re, err := regexp.Compile("(?i)" + r.Pattern)
		c.re = re
		return c, err
	}
	c.contains = text.Fold(r.Pattern)
	return c, nil
}

// Match renvoie la catégorie suggérée pour un libellé, 0 si aucune règle ne
// s'applique.
func (m *Matcher) Match(label string) int64 {
	folded, done := "", false
	for _, r := range m.rules {
		if r.re != nil {
			if r.re.MatchString(label) {
				return r.category
			}
			continue
		}
		if !done {
			folded, done = text.Fold(label), true
		}
		if strings.Contains(folded, r.contains) {
			return r.category
		}
	}
	return 0
}

// Matcher compile toutes les règles (ruleID = 0) ou une seule.
func (s *Service) Matcher(ctx context.Context, ruleID int64) (*Matcher, error) {
	var rules []Rule
	if ruleID == 0 {
		var err error
		if rules, err = s.Rules(ctx); err != nil {
			return nil, err
		}
	} else {
		r, err := s.Rule(ctx, ruleID)
		if err != nil {
			return nil, err
		}
		rules = []Rule{r}
	}
	m := &Matcher{}
	for _, r := range rules {
		c, err := compile(r)
		if err != nil {
			return nil, err // règle validée à l'enregistrement : inattendu
		}
		m.rules = append(m.rules, c)
	}
	return m, nil
}

// Apply applique les règles (toutes si ruleID vaut 0) aux opérations
// existantes : seulement à celles sans catégorie, ou à toutes si overwrite.
// Les jambes de virement ne sont jamais catégorisées. Renvoie le nombre
// d'opérations modifiées.
func (s *Service) Apply(ctx context.Context, ruleID int64, overwrite bool) (int, error) {
	m, err := s.Matcher(ctx, ruleID)
	if err != nil || len(m.rules) == 0 {
		return 0, err
	}
	byCategory, err := s.matches(ctx, m, overwrite)
	if err != nil {
		return 0, err
	}
	changed := 0
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		for category, ids := range byCategory {
			list, err := json.Marshal(ids)
			if err != nil {
				return err
			}
			res, err := tx.ExecContext(ctx,
				`UPDATE transactions SET category_id = ? WHERE transfer_id IS NULL AND id IN (SELECT value FROM json_each(?))`,
				category, string(list))
			if err != nil {
				return err
			}
			n, err := res.RowsAffected()
			if err != nil {
				return err
			}
			changed += int(n)
		}
		return nil
	})
	return changed, err
}

// matches parcourt les opérations concernées et regroupe par catégorie
// celles dont la catégorie changerait.
func (s *Service) matches(ctx context.Context, m *Matcher, overwrite bool) (map[int64][]int64, error) {
	query := `SELECT id, label, coalesce(category_id, 0) FROM transactions WHERE transfer_id IS NULL`
	if !overwrite {
		query += ` AND category_id IS NULL`
	}
	rows, err := s.db.R.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[int64][]int64{}
	for rows.Next() {
		var id, current int64
		var label string
		if err := rows.Scan(&id, &label, &current); err != nil {
			return nil, err
		}
		if c := m.Match(label); c != 0 && c != current {
			out[c] = append(out[c], id)
		}
	}
	return out, rows.Err()
}

// noise sont les mots que les banques ajoutent aux libellés sans qu'ils
// désignent le commerçant.
var noise = map[string]bool{
	"carte": true, "cb": true, "prlv": true, "prelevement": true, "sepa": true, "europeen": true,
	"vir": true, "virement": true, "recu": true, "emis": true, "inst": true, "instantane": true,
	"de": true, "du": true, "des": true, "la": true, "le": true, "les": true, "au": true,
	"retrait": true, "dab": true, "paiement": true, "achat": true, "facture": true, "motif": true,
}

// SuggestPattern propose le motif d'une règle d'après un libellé : les deux
// premiers mots significatifs, sans références ni dates (« CARTE X4321 30/10
// BOULANGERIE MARTIN 75 PARIS » → « BOULANGERIE MARTIN »).
func SuggestPattern(label string) string {
	var words []string
	for _, w := range strings.Fields(label) {
		w = strings.TrimFunc(w, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
		if w == "" || noise[text.Fold(w)] || strings.ContainsFunc(w, unicode.IsDigit) || utf8.RuneCountInString(w) < 2 {
			continue
		}
		if words = append(words, w); len(words) == 2 {
			break
		}
	}
	if len(words) == 0 {
		return strings.TrimSpace(label)
	}
	return strings.Join(words, " ")
}
