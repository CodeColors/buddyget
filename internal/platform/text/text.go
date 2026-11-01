// Package text normalise les textes pour les comparer : casse, accents,
// ligatures, apostrophes typographiques et espaces sont ignorés.
package text

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

var replacer = strings.NewReplacer("œ", "oe", "æ", "ae", "’", "'", "‘", "'")

// Fold renvoie la forme de comparaison d'un texte : « Détail de l’écriture »
// devient « detail de l'ecriture ».
func Fold(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, r := range norm.NFD.String(replacer.Replace(strings.ToLower(s))) {
		switch {
		case unicode.Is(unicode.Mn, r): // accent séparé de sa lettre
		case unicode.IsSpace(r):
			space = b.Len() > 0
		default:
			if space {
				b.WriteByte(' ')
				space = false
			}
			b.WriteRune(r)
		}
	}
	return b.String()
}
