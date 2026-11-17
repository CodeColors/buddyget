package web

import (
	"math"
	"net/http"
	"slices"

	"github.com/codecolors/buddyget/internal/alerts"
	"github.com/codecolors/buddyget/internal/forecast"
	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/reports"
)

// Le tableau de bord réunit, sans calcul propre, ce que les autres écrans
// détaillent : soldes, reste à vivre, mois en cours, solde prévu, échéances,
// dernières opérations, enveloppes et alertes.
const (
	dashboardShown   = 3  // enveloppes et alertes
	dashboardLines   = 5  // échéances et dernières opérations
	dashboardTop     = 3  // commerçants du mois
	dashboardHorizon = 45 // jours du solde prévu
	donutSlices      = 5  // catégories détaillées ; les suivantes sont regroupées
	donutGap         = .8 // espace entre deux arcs, sur 100
)

// Couleurs des arcs sans catégorie propre (attributs SVG, lisibles dans les
// deux thèmes).
const (
	uncategorizedColor = "#94a3b8"
	othersColor        = "#64748b"
)

// dashboardView est la donnée du tableau de bord.
type dashboardView struct {
	accountsView
	Outlook    forecast.Outlook
	HasOutlook bool
	LeftShare  float64 // part des entrées du mois qui reste à vivre, de 0 à 100 ; -1 sans entrée
	Month      reports.Report
	Flow       monthFlow
	Merchants  []reports.Merchant // où part l'argent ce mois-ci
	Donut      []donutSlice
	Forecast   *forecast.Result // solde prévu des comptes courants
	Chart      balanceChart
	Upcoming   []forecast.Line
	Recent     []ledger.Transaction
	Envelopes  []envelope // les plus consommées
	Alerts     []alerts.Alert
}

// monthFlow compare les revenus et dépenses du mois à ceux du mois
// précédent ; les largeurs de barre sont en % de la plus grande valeur.
type monthFlow struct {
	Income, Expenses, PrevIncome, PrevExpenses     int64
	IncomeW, ExpensesW, PrevIncomeW, PrevExpensesW float64
	Previous                                       calendar.Date // un jour du mois précédent
}

// donutSlice est un arc de l'anneau des dépenses, sur une circonférence de
// 100 : sa longueur (Dash) et son décalage (DashOffset, négatif : l'arc
// commence là où finit le précédent).
type donutSlice struct {
	Name             string
	Color            string
	Amount           int64
	Share            int // part des dépenses, en % arrondi
	Dash, DashOffset float64
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	v, err := s.loadDashboard(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "dashboard", "Tableau de bord", v)
}

func (s *Server) loadDashboard(r *http.Request) (dashboardView, error) {
	ctx := r.Context()
	var v dashboardView
	var err error
	if v.accountsView, err = s.loadAccounts(r); err != nil || len(v.Active) == 0 {
		return v, err
	}
	if v.Outlook, v.HasOutlook, err = s.forecast.Outlook(ctx, v.Today); err != nil {
		return v, err
	}
	if v.HasOutlook {
		v.LeftShare = leftShare(v.Outlook)
		fc, err := s.forecast.Forecast(ctx, forecast.Query{Target: v.Today.AddDays(dashboardHorizon)}, v.Today)
		if err != nil {
			return v, err
		}
		v.Forecast, v.Chart = &fc.Result, newBalanceChart(fc.Result, nil)
		v.Upcoming = upcoming(fc.Lines, dashboardLines)
	}
	if v.Month, err = s.reports.Monthly(ctx, v.Today); err != nil {
		return v, err
	}
	v.Flow, v.Donut = newMonthFlow(v.Month), newDonut(v.Month)
	v.Merchants = v.Month.Merchants[:min(len(v.Month.Merchants), dashboardTop)]
	page, err := s.ledger.Search(ctx, ledger.Filter{To: v.Today}, 0)
	if err != nil {
		return v, err
	}
	v.Recent = page.Items[:min(len(page.Items), dashboardLines)]
	if v.Envelopes, err = s.envelopes(r, v.Today); err != nil {
		return v, err
	}
	slices.SortStableFunc(v.Envelopes, func(a, b envelope) int { return b.Percent() - a.Percent() })
	v.Envelopes = v.Envelopes[:min(len(v.Envelopes), dashboardShown)]
	if v.Alerts, err = s.alerts.Open(ctx); err != nil {
		return v, err
	}
	v.Alerts = v.Alerts[:min(len(v.Alerts), dashboardShown)]
	return v, nil
}

// leftShare renvoie la part des entrées du mois (reçues et attendues) qui
// reste à vivre, de 0 à 100 ; -1 sans entrée.
func leftShare(o forecast.Outlook) float64 {
	in := o.Received + o.Expected
	if in <= 0 {
		return -1
	}
	return math.Max(0, math.Min(100, float64(o.Left())*100/float64(in)))
}

// upcoming renvoie les n premières échéances comptées.
func upcoming(lines []forecast.Line, n int) []forecast.Line {
	var out []forecast.Line
	for _, l := range lines {
		if len(out) == n {
			break
		}
		if !l.Excluded {
			out = append(out, l)
		}
	}
	return out
}

func newMonthFlow(m reports.Report) monthFlow {
	f := monthFlow{
		Income: m.Income, Expenses: m.Expenses,
		PrevIncome: m.Previous.Income, PrevExpenses: m.Previous.Expenses,
		Previous: m.From.AddDays(-1),
	}
	top := max(f.Income, f.Expenses, f.PrevIncome, f.PrevExpenses)
	if top <= 0 {
		return f
	}
	width := func(v int64) float64 { return float64(max(v, 0)) * 100 / float64(top) }
	f.IncomeW, f.ExpensesW = width(f.Income), width(f.Expenses)
	f.PrevIncomeW, f.PrevExpensesW = width(f.PrevIncome), width(f.PrevExpenses)
	return f
}

// newDonut découpe les dépenses du mois par catégorie : les donutSlices
// premières, puis les autres regroupées.
func newDonut(m reports.Report) []donutSlice {
	var out []donutSlice
	var total, others int64
	for _, c := range m.Categories {
		if c.Amount <= 0 {
			continue
		}
		total += c.Amount
		if len(out) == donutSlices {
			others += c.Amount
			continue
		}
		sl := donutSlice{Name: c.Name, Color: c.Color, Amount: c.Amount}
		if sl.Name == "" {
			sl.Name, sl.Color = "Sans catégorie", uncategorizedColor
		}
		out = append(out, sl)
	}
	if others > 0 {
		out = append(out, donutSlice{Name: "Autres catégories", Color: othersColor, Amount: others})
	}
	offset := 0.0
	for i := range out {
		share := float64(out[i].Amount) * 100 / float64(total)
		out[i].Share = int(math.Round(share))
		out[i].DashOffset, out[i].Dash = -offset, share
		if len(out) > 1 {
			out[i].Dash = math.Max(share-donutGap, .4)
		}
		offset += share
	}
	return out
}
