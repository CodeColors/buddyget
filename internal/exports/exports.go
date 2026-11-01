// Package exports est le point d'entrée unique des exports : opérations
// en CSV, rapports en CSV ou PDF, dossier ZIP pour un comptable ou la
// déclaration d'impôts. Tout est produit en flux vers le client : les
// opérations sont lues au fil de l'écriture, les pièces jointes copiées
// une à une.
package exports

import (
	"encoding/csv"
	"io"
	"strconv"
	"strings"

	"github.com/codecolors/buddyget/internal/attachments"
	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/platform/money"
)

// Service produit les exports qui lisent la base : opérations et dossier.
// Les rapports, déjà calculés par le package reports, s'exportent avec
// ReportCSV et ReportPDF.
type Service struct {
	ledger      *ledger.Service
	attachments *attachments.Service
}

// NewService crée le service des exports.
func NewService(l *ledger.Service, a *attachments.Service) *Service {
	return &Service{ledger: l, attachments: a}
}

// csvWriter écrit un CSV lisible par un tableur français : UTF-8 avec BOM,
// point-virgule, virgule décimale. Les textes qui commenceraient par un
// signe de formule sont neutralisés (injection de formules).
type csvWriter struct{ w *csv.Writer }

func newCSV(w io.Writer) (*csvWriter, error) {
	if _, err := io.WriteString(w, "\xef\xbb\xbf"); err != nil {
		return nil, err
	}
	c := csv.NewWriter(w)
	c.Comma = ';'
	return &csvWriter{w: c}, nil
}

func (c *csvWriter) row(cells ...string) error { return c.w.Write(cells) }

func (c *csvWriter) flush() error {
	c.w.Flush()
	return c.w.Error()
}

// text neutralise une cellule de texte libre.
func text(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

// amount écrit un montant signé : « -1234,56 ».
func amount(cents int64) string {
	if cents < 0 {
		return "-" + money.FormatInput(cents)
	}
	return money.FormatInput(cents)
}

// operationsHeader et operationRow décrivent une opération exportée ;
// files nomme ses pièces jointes dans le dossier ZIP.
var operationsHeader = []string{"Date", "Compte", "Libellé", "Montant", "Catégorie", "Étiquettes", "Note", "Pointée", "Virement avec"}

func operationRow(t ledger.Transaction) []string {
	cleared := "non"
	if t.Cleared {
		cleared = "oui"
	}
	return []string{
		t.Date.Format(), text(t.AccountName), text(t.Label), amount(t.Amount), text(t.CategoryName),
		text(strings.Join(t.Tags, ", ")), text(t.Note), cleared, text(t.CounterpartAccountName),
	}
}

// percent écrit un pourcentage entier.
func percent(p int) string { return strconv.Itoa(p) + " %" }
