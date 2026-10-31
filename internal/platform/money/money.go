// Package money manipule les montants en centimes (int64) : lecture d'une
// saisie à la française et affichage. Aucun flottant n'intervient.
package money

import (
	"strconv"
	"strings"

	"github.com/codecolors/buddyget/internal/platform/validation"
)

// Max est la valeur absolue maximale acceptée (999 999 999,99 €).
const Max = 99_999_999_999

const (
	thinSpace = "\u202f" // séparateur de milliers (espace fine insécable)
	nbsp      = "\u00a0" // avant le symbole €
	minus     = "\u2212" // signe moins typographique
)

// Parse lit un montant saisi : « 12 », « 12,5 », « -1 234,56 », « 12.50 »,
// « 12€50 », « 1.234,56 » ou « 1,234.56 ». Quand virgule et point sont
// présents, le dernier est le séparateur décimal ; seul, il doit être suivi
// d'au plus deux chiffres.
func Parse(s string) (int64, error) {
	if before, after, ok := strings.Cut(strings.TrimSpace(s), "€"); ok && after != "" {
		s = before + "," + after // « 12€50 »
	}
	s = strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\u00a0', '\u202f', '€':
			return -1
		case '\u2212':
			return '-'
		}
		return r
	}, s)
	if s == "" {
		return 0, validation.Error("Indiquez un montant.")
	}
	negative := false
	switch s[0] {
	case '-':
		negative, s = true, s[1:]
	case '+':
		s = s[1:]
	}

	intPart, frac := s, ""
	if i := strings.LastIndexAny(s, ".,"); i >= 0 {
		sep := s[i]
		other := byte('.')
		if sep == '.' {
			other = ','
		}
		intPart, frac = s[:i], s[i+1:]
		if strings.IndexByte(intPart, other) >= 0 {
			intPart = strings.ReplaceAll(intPart, string(other), "")
		} else if strings.IndexByte(intPart, sep) >= 0 {
			return 0, invalid()
		}
		if len(frac) == 0 || len(frac) > 2 {
			return 0, validation.Error("Montant invalide : deux décimales au maximum.")
		}
	}
	if intPart == "" {
		intPart = "0"
	}
	if !digitsOnly(intPart) || !digitsOnly(frac) || len(intPart) > 9 {
		return 0, invalid()
	}
	units, _ := strconv.ParseInt(intPart, 10, 64)
	cents, _ := strconv.ParseInt((frac + "00")[:2], 10, 64)
	v := units*100 + cents
	if v > Max {
		return 0, validation.Error("Montant trop élevé.")
	}
	if negative {
		v = -v
	}
	return v, nil
}

func invalid() error {
	return validation.Error("Montant invalide : saisissez par exemple 12,50.")
}

func digitsOnly(s string) bool {
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// Format affiche un montant : « 1 234,56 € », « −12,00 € ».
func Format(cents int64) string {
	return format(cents, thinSpace) + nbsp + "€"
}

// FormatSigned affiche le signe des montants positifs : « +12,00 € ».
func FormatSigned(cents int64) string {
	if cents > 0 {
		return "+" + Format(cents)
	}
	return Format(cents)
}

// FormatInput renvoie la valeur absolue à replacer dans un champ de saisie :
// « 1234,56 ».
func FormatInput(cents int64) string {
	if cents < 0 {
		cents = -cents
	}
	return format(cents, "")
}

func format(cents int64, group string) string {
	sign := ""
	u := uint64(cents) //nolint:gosec // G115 : valeur absolue calculée ci-dessous.
	if cents < 0 {
		sign = minus
		u = -u
	}
	digits := strconv.FormatUint(u/100, 10)
	var b strings.Builder
	b.WriteString(sign)
	for i, d := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteString(group)
		}
		b.WriteRune(d)
	}
	b.WriteByte(',')
	c := u % 100
	b.WriteByte(byte('0' + c/10))
	b.WriteByte(byte('0' + c%10))
	return b.String()
}
