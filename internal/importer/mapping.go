package importer

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/platform/money"
	"github.com/codecolors/buddyget/internal/platform/text"
	"github.com/codecolors/buddyget/internal/platform/validation"
)

// NoColumn marque une colonne non utilisée.
const NoColumn = -1

// Mapping est l'unique consigne de lecture d'un fichier : un profil se
// résout en Mapping d'après la ligne d'en-tête, une correspondance manuelle
// en produit un directement. Les colonnes sont numérotées à partir de 0.
type Mapping struct {
	HeaderLine int    `json:"header_line"` // ligne d'en-tête (0 : aucune)
	Date       int    `json:"date"`
	Labels     []int  `json:"labels"` // la première colonne non vide l'emporte
	Amount     int    `json:"amount"` // NoColumn si débit et crédit séparés
	Debit      int    `json:"debit"`
	Credit     int    `json:"credit"`
	DateFormat string `json:"date_format"` // "" : reconnaissance automatique
	Invert     bool   `json:"invert"`      // dépenses en positif dans le fichier
}

// DateFormats associe les formats proposés à leur gabarit Go.
var DateFormats = []struct{ Label, Layout string }{
	{"JJ/MM/AAAA", "02/01/2006"},
	{"JJ/MM/AA", "02/01/06"},
	{"AAAA-MM-JJ", "2006-01-02"},
	{"JJ-MM-AAAA", "02-01-2006"},
	{"JJ.MM.AAAA", "02.01.2006"},
	{"AAAA/MM/JJ", "2006/01/02"},
}

// validate vérifie qu'une correspondance désigne de quoi lire une opération.
func (m Mapping) validate() error {
	switch {
	case m.Date < 0:
		return validation.Error("Indiquez la colonne de la date.")
	case len(m.Labels) == 0:
		return validation.Error("Indiquez la colonne du libellé.")
	case m.Amount < 0 && m.Debit < 0 && m.Credit < 0:
		return validation.Error("Indiquez la colonne du montant, ou celles du débit et du crédit.")
	}
	if m.DateFormat != "" && layout(m.DateFormat) == "" {
		return validation.Error("Format de date inconnu.")
	}
	return nil
}

func layout(label string) string {
	for _, f := range DateFormats {
		if f.Label == label {
			return f.Layout
		}
	}
	return ""
}

// Line est une ligne de fichier interprétée ; Err explique pourquoi elle ne
// peut pas être importée.
type Line struct {
	Number int
	Date   calendar.Date
	Label  string
	Amount int64
	Err    string
}

// parse interprète une ligne de données. ok est faux pour une ligne vide,
// simplement ignorée. Les classeurs fournissent des nombres bruts : dates en
// numéro de série, montants éventuellement en virgule flottante.
func (m Mapping) parse(number int, cells []string, spreadsheet bool) (l Line, ok bool) {
	l.Number = number
	if blank(cells) {
		return l, false
	}
	cell := func(i int) string {
		if i < 0 || i >= len(cells) {
			return ""
		}
		return strings.TrimSpace(cells[i])
	}

	date, err := parseDate(cell(m.Date), m.DateFormat, spreadsheet)
	if err != nil {
		l.Err = fmt.Sprintf("Date illisible « %s ».", cell(m.Date))
		return l, true
	}
	l.Date = date

	for _, i := range m.Labels {
		if v := cell(i); v != "" {
			l.Label = strings.Join(strings.Fields(v), " ")
			break
		}
	}
	if l.Label == "" {
		l.Err = "Libellé absent."
		return l, true
	}

	if l.Amount, err = m.amount(cell, spreadsheet); err != nil {
		l.Err = err.Error()
		return l, true
	}
	if m.Invert {
		l.Amount = -l.Amount
	}
	if l.Amount == 0 {
		l.Err = "Montant nul."
	}
	return l, true
}

func (m Mapping) amount(cell func(int) string, spreadsheet bool) (int64, error) {
	if m.Amount >= 0 {
		return parseAmount(cell(m.Amount), spreadsheet)
	}
	if v := cell(m.Debit); v != "" {
		d, err := parseAmount(v, spreadsheet)
		return -max(d, -d), err
	}
	if v := cell(m.Credit); v != "" {
		c, err := parseAmount(v, spreadsheet)
		return max(c, -c), err
	}
	return 0, validation.Error("Montant absent.")
}

func blank(cells []string) bool {
	for _, c := range cells {
		if strings.TrimSpace(c) != "" {
			return false
		}
	}
	return true
}

// excelEpoch est l'origine des numéros de série de date d'Excel (système
// 1900, décalage du faux 29/02/1900 compris).
var excelEpoch = time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)

func parseDate(s, format string, spreadsheet bool) (calendar.Date, error) {
	if i := strings.IndexByte(s, ' '); i > 0 {
		s = s[:i] // « 31/10/2026 00:00:00 »
	}
	if spreadsheet {
		if serial, err := strconv.ParseFloat(s, 64); err == nil && serial >= 1 && serial < 2_958_466 {
			return calendar.FromTime(excelEpoch.AddDate(0, 0, int(serial))), nil
		}
	}
	layouts := []string{layout(format)}
	if format == "" {
		layouts = layouts[:0]
		for _, f := range DateFormats {
			layouts = append(layouts, f.Layout)
		}
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			return calendar.FromTime(t), nil
		}
	}
	return "", validation.Error("Date illisible.")
}

// parseAmount lit un montant. Dans un classeur, un nombre stocké en virgule
// flottante (« 12.299999999999999 ») est arrondi au centime.
func parseAmount(s string, spreadsheet bool) (int64, error) {
	if s == "" {
		return 0, validation.Error("Montant absent.")
	}
	v, err := money.Parse(s)
	if err == nil || !spreadsheet {
		return v, err
	}
	f, ferr := strconv.ParseFloat(s, 64)
	if ferr != nil || math.Abs(f) > money.Max/100 {
		return 0, err
	}
	return int64(math.Round(f * 100)), nil
}

// dedupKey identifie une opération dans un fichier : deux lignes identiques
// (date, montant, libellé normalisé) sont distinguées par leur rang.
func dedupKey(l Line) string {
	return fmt.Sprintf("%s|%d|%s", l.Date, l.Amount, text.Fold(l.Label))
}

// lineHash est l'empreinte stockée avec l'opération importée. Le rang
// d'occurrence évite de fusionner deux achats identiques le même jour, tout
// en rendant le réimport d'un fichier qui chevauche le précédent sans effet.
func lineHash(accountID int64, key string, rank int) string {
	sum := sha256.Sum256(fmt.Appendf(nil, "%d|%s|%d", accountID, key, rank))
	return hex.EncodeToString(sum[:16])
}
