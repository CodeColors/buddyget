// Package ledger tient le grand livre du foyer : comptes, catégories,
// opérations et virements internes. Toute lecture ou écriture de ces
// données passe par son Service.
package ledger

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/codecolors/buddyget/internal/platform/db"
	"github.com/codecolors/buddyget/internal/platform/validation"
)

// ErrNotFound signale un compte, une catégorie ou une opération inexistants.
var ErrNotFound = errors.New("élément introuvable")

// Service porte la logique du grand livre.
type Service struct {
	db *db.DB
}

// NewService crée le service du grand livre.
func NewService(d *db.DB) *Service { return &Service{db: d} }

// cleanText nettoie un champ texte et contrôle sa longueur.
func cleanText(s, field string, maxLen int, required bool) (string, error) {
	s = strings.TrimSpace(s)
	if required && s == "" {
		return "", validation.Errorf("Indiquez %s.", field)
	}
	if utf8.RuneCountInString(s) > maxLen {
		return "", validation.Errorf("Le champ %s ne doit pas dépasser %d caractères.", field, maxLen)
	}
	return s, nil
}

// querier est commun à *sql.DB et *sql.Tx.
type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// exists vérifie la présence d'une ligne ; query doit renvoyer un booléen.
func exists(ctx context.Context, q querier, query string, args ...any) (bool, error) {
	var ok bool
	err := q.QueryRowContext(ctx, query, args...).Scan(&ok)
	return ok, err
}

// foldReplacer ramène les lettres accentuées du français à leur base.
var foldReplacer = strings.NewReplacer(
	"à", "a", "â", "a", "ä", "a", "ç", "c", "é", "e", "è", "e", "ê", "e", "ë", "e",
	"î", "i", "ï", "i", "ô", "o", "ö", "o", "ù", "u", "û", "u", "ü", "u", "ÿ", "y",
	"œ", "oe", "æ", "ae",
)

// sortKey rend les noms comparables dans l'ordre alphabétique français
// (casse et accents ignorés) : « Épargne » se range avant « Loisirs ».
func sortKey(s string) string { return foldReplacer.Replace(strings.ToLower(s)) }
