package web

import (
	"slices"
	"testing"

	"github.com/codecolors/buddyget/internal/forecast"
	"github.com/codecolors/buddyget/internal/reports"
)

func TestLeftShare(t *testing.T) {
	for _, c := range []struct {
		o    forecast.Outlook
		want float64
	}{
		{forecast.Outlook{Received: 2000_00, Expected: 0, Spent: -500_00}, 75},
		{forecast.Outlook{Received: 1000_00, Expected: 1000_00, Upcoming: -1500_00}, 25},
		{forecast.Outlook{Received: 1000_00, Spent: -1500_00}, 0}, // dépassement
		{forecast.Outlook{Spent: -100_00}, -1},                    // sans entrée
	} {
		if got := leftShare(c.o); got != c.want {
			t.Errorf("leftShare(%+v) = %v, attendu %v", c.o, got, c.want)
		}
	}
}

func TestDonut(t *testing.T) {
	m := reports.Report{Expenses: 1000_00}
	for _, a := range []int64{400_00, 200_00, 150_00, 100_00, 50_00, 60_00, 40_00} {
		m.Categories = append(m.Categories, reports.CategoryLine{Name: "C", Color: "#123456", Amount: a})
	}
	m.Categories = append(m.Categories, reports.CategoryLine{Name: "Remboursée", Amount: -10_00, Previous: 30_00})
	d := newDonut(m)
	if len(d) != donutSlices+1 || d[donutSlices].Name != "Autres catégories" || d[donutSlices].Amount != 100_00 {
		t.Fatalf("regroupement : %+v", d)
	}
	if d[0].Share != 40 || d[0].DashOffset != 0 || d[1].DashOffset != -40 || d[0].Dash != 40-donutGap {
		t.Errorf("arcs : %+v", d[:2])
	}
	single := newDonut(reports.Report{Expenses: 50_00, Categories: []reports.CategoryLine{{Amount: 50_00}}})
	if len(single) != 1 || single[0].Name != "Sans catégorie" || single[0].Color != uncategorizedColor || single[0].Dash != 100 {
		t.Errorf("une seule catégorie, sans nom : %+v", single)
	}
	if newDonut(reports.Report{}) != nil {
		t.Error("aucune dépense : pas d'anneau")
	}
}

func TestMonthFlow(t *testing.T) {
	f := newMonthFlow(reports.Report{From: "2026-11-01", Income: 1000_00, Expenses: 500_00, Previous: reports.Totals{Income: 2000_00, Expenses: 1500_00}})
	if f.IncomeW != 50 || f.ExpensesW != 25 || f.PrevIncomeW != 100 || f.PrevExpensesW != 75 || f.Previous.MonthLabel() != "octobre 2026" {
		t.Errorf("flux : %+v", f)
	}
	if f := newMonthFlow(reports.Report{From: "2026-11-01"}); f.IncomeW != 0 || f.ExpensesW != 0 {
		t.Errorf("mois vide : %+v", f)
	}
}

func TestUpcoming(t *testing.T) {
	lines := []forecast.Line{
		{Event: forecast.Event{Label: "A"}},
		{Event: forecast.Event{Label: "B", Excluded: true}},
		{Event: forecast.Event{Label: "C"}},
		{Event: forecast.Event{Label: "D"}},
	}
	var labels []string
	for _, l := range upcoming(lines, 2) {
		labels = append(labels, l.Label)
	}
	if !slices.Equal(labels, []string{"A", "C"}) {
		t.Errorf("échéances : %v", labels)
	}
}
