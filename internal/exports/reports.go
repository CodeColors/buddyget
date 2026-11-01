package exports

import (
	"io"
	"strconv"
	"time"

	"github.com/codecolors/buddyget/internal/platform/money"
	"github.com/codecolors/buddyget/internal/reports"
)

// categoryName nomme une ligne de catégorie, « Sans catégorie » à défaut.
func categoryName(c reports.CategoryLine) string {
	if c.Name == "" {
		return "Sans catégorie"
	}
	return c.Name
}

// ReportCSV écrit un rapport en CSV, une section par ligne (Section ;
// Libellé ; Montant ; Précédent ; Détail).
func ReportCSV(w io.Writer, title string, r reports.Report) error {
	c, err := newCSV(w)
	if err != nil {
		return err
	}
	rows := [][]string{
		{"Section", "Libellé", "Montant", "Période précédente", "Détail"},
		{"Rapport", text(title), "", "", r.From.Format() + " au " + r.To.Format()},
		{"Total", "Revenus", amount(r.Income), amount(r.Previous.Income), ""},
		{"Total", "Dépenses", amount(r.Expenses), amount(r.Previous.Expenses), ""},
		{"Total", "Épargné", amount(r.Saved()), "", percent(r.SavingsRate()) + " des revenus"},
	}
	for _, l := range r.Categories {
		rows = append(rows, []string{"Catégorie", text(categoryName(l)), amount(l.Amount), amount(l.Previous), percent(l.Share) + " des dépenses"})
	}
	for _, m := range r.Merchants {
		rows = append(rows, []string{"Commerçant", text(m.Name), amount(m.Amount), "", strconv.Itoa(m.Count) + " opérations"})
	}
	for _, t := range r.Tags {
		rows = append(rows, []string{"Étiquette", text(t.Name), amount(t.Expenses), "", "revenus " + amount(t.Income)})
	}
	for _, m := range r.Months {
		rows = append(rows, []string{"Mois", m.Month.MonthLabel(), amount(m.Expenses), "", "revenus " + amount(m.Income)})
	}
	for _, row := range rows {
		if err := c.row(row...); err != nil {
			return err
		}
	}
	return c.flush()
}

// ReportPDF écrit un rapport en PDF.
func ReportPDF(w io.Writer, title string, r reports.Report, now time.Time) error {
	d := newDocument(title, "Du "+r.From.Format()+" au "+r.To.Format()+", hors virements entre vos comptes.", now)
	figures := [][2]string{
		{"Revenus", money.Format(r.Income)},
		{"Dépenses", money.Format(r.Expenses)},
		{"Épargné", money.FormatSigned(r.Saved())},
	}
	if r.Income > 0 {
		figures = append(figures, [2]string{"Taux d'épargne", percent(r.SavingsRate())})
	}
	if r.HasPrevious {
		figures = append(figures, [2]string{"Période précédente", "revenus " + money.Format(r.Previous.Income) + ", dépenses " + money.Format(r.Previous.Expenses)})
	}
	d.figures(figures)
	if len(r.Categories) > 0 {
		d.heading("Dépenses par catégorie")
		var rows [][]string
		for _, l := range r.Categories {
			rows = append(rows, []string{categoryName(l), money.Format(l.Amount), money.Format(l.Previous), percent(l.Share)})
		}
		d.table([]column{{"Catégorie", 90, "L"}, {"Montant", 32, "R"}, {"Précédent", 32, "R"}, {"Part", 26, "R"}}, rows)
	}
	if len(r.Months) > 0 {
		d.heading("Mois par mois")
		var rows [][]string
		for _, m := range r.Months {
			rows = append(rows, []string{m.Month.MonthLabel(), money.Format(m.Income), money.Format(m.Expenses)})
		}
		d.table([]column{{"Mois", 90, "L"}, {"Revenus", 45, "R"}, {"Dépenses", 45, "R"}}, rows)
	}
	if len(r.Merchants) > 0 {
		d.heading("Principaux commerçants")
		var rows [][]string
		for _, m := range r.Merchants {
			rows = append(rows, []string{m.Name, strconv.Itoa(m.Count), money.Format(m.Amount)})
		}
		d.table([]column{{"Commerçant", 120, "L"}, {"Opérations", 25, "R"}, {"Montant", 35, "R"}}, rows)
	}
	if len(r.Tags) > 0 {
		d.heading("Par étiquette")
		var rows [][]string
		for _, t := range r.Tags {
			rows = append(rows, []string{t.Name, money.Format(t.Expenses), money.Format(t.Income)})
		}
		d.table([]column{{"Étiquette", 90, "L"}, {"Dépenses", 45, "R"}, {"Revenus", 45, "R"}}, rows)
	}
	return d.write(w)
}
