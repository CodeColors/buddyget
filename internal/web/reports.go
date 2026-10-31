package web

import (
	"net/http"
	"strconv"

	"github.com/codecolors/buddyget/internal/forecast"
	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/platform/money"
	"github.com/codecolors/buddyget/internal/reports"
)

// reportView est la donnée de la page des rapports : un mois (?mois=AAAA-MM)
// ou une année (?annee=AAAA).
type reportView struct {
	reports.Report
	Annual     bool
	Period     string // « octobre 2026 », « 2026 »
	Prev, Next string // adresses des périodes voisines
	Chart      *barChart
}

func (s *Server) reportsPage(w http.ResponseWriter, r *http.Request) {
	today := calendar.Today()
	var v reportView
	var err error
	if y := r.FormValue("annee"); y != "" {
		year, convErr := strconv.Atoi(y)
		if convErr != nil || year < 1900 || year > 9999 {
			year = today.Time().Year()
		}
		v.Annual, v.Period = true, strconv.Itoa(year)
		v.Prev, v.Next = "/rapports?annee="+strconv.Itoa(year-1), "/rapports?annee="+strconv.Itoa(year+1)
		if v.Report, err = s.reports.Annual(r.Context(), year); err == nil {
			v.Chart = monthsChart(v.Report)
		}
	} else {
		day := today
		if d, parseErr := calendar.Parse(r.FormValue("mois") + "-01"); parseErr == nil {
			day = d
		}
		t := day.Time()
		month := func(offset int) string {
			return "/rapports?mois=" + string(calendar.FromTime(t.AddDate(0, offset, 1-t.Day())))[:7]
		}
		v.Period, v.Prev, v.Next = day.MonthLabel(), month(-1), month(1)
		v.Report, err = s.reports.Monthly(r.Context(), day)
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "reports", "Rapports", v)
}

// monthsChart compare les dépenses de chaque mois aux revenus mensuels
// moyens de l'année.
func monthsChart(r reports.Report) *barChart {
	points := make([]forecast.Point, len(r.Months))
	for i, m := range r.Months {
		points[i] = forecast.Point{Date: m.Month, Balance: m.Expenses}
	}
	average := r.Income / int64(max(len(r.Months), 1))
	c := newBarChart("mois", points, average)
	c.Desc = "Dépenses de chaque mois ; la ligne marque les revenus mensuels moyens de l'année (" +
		money.Format(average) + "). Le tableau suit le graphique."
	return &c
}
