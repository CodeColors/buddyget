package exports

import (
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/go-pdf/fpdf"
)

// Les PDF utilisent les polices standard du format (Helvetica), sans
// police embarquée : le texte est converti en Windows-1252, qui couvre le
// français et le signe euro.

const (
	pageMargin = 15.0 // mm
	lineHeight = 6.0  // mm
)

// compressPDF compresse le contenu des pages (désactivé par les tests, qui
// lisent le texte produit).
var compressPDF = true

// latin remplace les caractères absents de Windows-1252 par leur plus
// proche équivalent.
var latin = strings.NewReplacer(" ", " ", " ", " ", "−", "-", "›", ">")

// document est un PDF A4 simple : titre, sous-titres, tableaux.
type document struct {
	pdf *fpdf.Fpdf
	tr  func(string) string
}

// column décrit une colonne de tableau : largeur en mm, alignement
// (L, C ou R).
type column struct {
	Title string
	Width float64
	Align string
}

func newDocument(title, subtitle string, now time.Time) *document {
	p := fpdf.New("P", "mm", "A4", "")
	p.SetCompression(compressPDF)
	p.SetMargins(pageMargin, pageMargin, pageMargin)
	p.SetAutoPageBreak(true, pageMargin+5)
	p.SetCreator("BuddyGet", true)
	p.SetTitle(title, true)
	p.AliasNbPages("")
	d := &document{pdf: p}
	tr := p.UnicodeTranslatorFromDescriptor("")
	d.tr = func(s string) string { return tr(latin.Replace(s)) }
	generated := "BuddyGet, " + now.Format("02/01/2006 15:04")
	p.SetFooterFunc(func() {
		p.SetY(-pageMargin)
		p.SetFont("Helvetica", "", 8)
		p.SetTextColor(110, 110, 110)
		p.CellFormat(0, 4, d.tr(generated), "", 0, "L", false, 0, "")
		p.SetX(pageMargin)
		p.CellFormat(0, 4, "Page "+strconv.Itoa(p.PageNo())+"/{nb}", "", 0, "R", false, 0, "")
	})
	p.AddPage()
	p.SetFont("Helvetica", "B", 16)
	p.SetTextColor(20, 20, 20)
	p.CellFormat(0, 9, d.tr(title), "", 1, "L", false, 0, "")
	if subtitle != "" {
		p.SetFont("Helvetica", "", 10)
		p.SetTextColor(90, 90, 90)
		p.MultiCell(0, 5, d.tr(subtitle), "", "L", false)
	}
	p.Ln(3)
	return d
}

// heading écrit un intertitre.
func (d *document) heading(text string) {
	d.pdf.Ln(3)
	d.pdf.SetFont("Helvetica", "B", 12)
	d.pdf.SetTextColor(20, 20, 20)
	d.pdf.CellFormat(0, 8, d.tr(text), "", 1, "L", false, 0, "")
}

// figures écrit des paires libellé / valeur.
func (d *document) figures(pairs [][2]string) {
	d.pdf.SetFont("Helvetica", "", 10)
	for _, p := range pairs {
		d.pdf.SetTextColor(90, 90, 90)
		d.pdf.CellFormat(70, lineHeight, d.tr(p[0]), "", 0, "L", false, 0, "")
		d.pdf.SetTextColor(20, 20, 20)
		d.pdf.CellFormat(0, lineHeight, d.tr(p[1]), "", 1, "L", false, 0, "")
	}
}

// table écrit un tableau ; l'en-tête est répété en haut de chaque page.
func (d *document) table(cols []column, rows [][]string) {
	header := func() {
		d.pdf.SetFont("Helvetica", "B", 9)
		d.pdf.SetFillColor(235, 237, 240)
		d.pdf.SetTextColor(40, 40, 40)
		for _, c := range cols {
			d.pdf.CellFormat(c.Width, lineHeight, d.tr(c.Title), "B", 0, c.Align, true, 0, "")
		}
		d.pdf.Ln(-1)
		d.pdf.SetFont("Helvetica", "", 9)
		d.pdf.SetTextColor(20, 20, 20)
	}
	header()
	_, pageHeight := d.pdf.GetPageSize()
	for _, row := range rows {
		if d.pdf.GetY()+lineHeight > pageHeight-pageMargin-5 {
			d.pdf.AddPage()
			header()
		}
		for i, c := range cols {
			text := d.tr(row[i])
			// Un texte trop long est tronqué : le tableau garde une ligne par
			// entrée.
			for d.pdf.GetStringWidth(text) > c.Width-2 && len(text) > 1 {
				text = text[:len(text)-2] + "."
			}
			d.pdf.CellFormat(c.Width, lineHeight, text, "B", 0, c.Align, false, 0, "")
		}
		d.pdf.Ln(-1)
	}
}

// note écrit un paragraphe discret.
func (d *document) note(text string) {
	d.pdf.Ln(2)
	d.pdf.SetFont("Helvetica", "I", 9)
	d.pdf.SetTextColor(90, 90, 90)
	d.pdf.MultiCell(0, 5, d.tr(text), "", "L", false)
}

// write produit le PDF.
func (d *document) write(w io.Writer) error { return d.pdf.Output(w) }
