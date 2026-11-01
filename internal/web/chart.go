package web

import (
	"math"
	"strconv"
	"strings"

	"github.com/codecolors/buddyget/internal/forecast"
	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/platform/money"
)

// Le graphique est un SVG rendu côté serveur, sans script ni style en
// ligne (CSP) : la courbe est tracée dans un repère de 1000 × 1000 étiré à
// la taille du cadre, les textes et le point bas sont placés en
// pourcentages pour ne pas être déformés.
const (
	plotSize   = 1000.0
	plotTop    = 8.0  // marge haute, en % de la hauteur du cadre
	plotHeight = 80.0 // hauteur du tracé, en % ; dessous, les mois
	maxXTicks  = 6
)

// balanceChart est une courbe de soldes datés.
type balanceChart struct {
	ID          string  // préfixe des identifiants (titre, description)
	Desc        string  // description textuelle de la courbe
	Top, Height float64 // cadre du tracé, en %
	Line, Area  string  // tracés (attribut d), repère 1000 × 1000
	Reference   string  // courbe de référence en pointillés (scénario), facultative
	// Seuil, périodes sous le seuil et point bas : prévisionnel seulement.
	HasThreshold bool
	Threshold    float64
	Below        []chartBand
	HasLow       bool
	LowX, LowY   float64 // point bas, en %
	YTicks       []chartTick
	XTicks       []chartTick
}

// chartBand est une bande verticale (période sous le seuil), repère 1000.
type chartBand struct{ X, W float64 }

// chartTick est une graduation : position dans le repère 1000 (Pos) et en %
// du cadre (Pct).
type chartTick struct {
	Pos, Pct float64
	Label    string
}

// chartScale place dates et montants dans le repère du tracé.
type chartScale struct {
	first  calendar.Date
	days   float64
	lo, hi int64
}

func (s chartScale) x(d calendar.Date) float64 {
	return plotSize * float64(s.first.DaysUntil(d)) / s.days
}

func (s chartScale) y(v int64) float64 { return plotSize * float64(s.hi-v) / float64(s.hi-s.lo) }

// lineChart trace une série de soldes datés, croissante et non vide : en
// escalier (soldes jour par jour) ou en ligne brisée (soldes de fin de mois).
// include étend l'axe des montants (un seuil, par exemple).
func lineChart(id string, points []forecast.Point, step bool, include ...int64) (balanceChart, chartScale) {
	lo, hi := points[0].Balance, points[0].Balance
	for _, p := range points {
		lo, hi = min(lo, p.Balance), max(hi, p.Balance)
	}
	for _, v := range include {
		lo, hi = min(lo, v), max(hi, v)
	}
	unit := niceStep(hi - lo)
	sc := chartScale{first: points[0].Date, days: float64(max(points[0].Date.DaysUntil(points[len(points)-1].Date), 1))}
	sc.lo, sc.hi = floorTo(lo, unit), -floorTo(-hi, unit)
	if sc.hi == sc.lo {
		sc.hi = sc.lo + unit
	}

	c := balanceChart{ID: id, Top: plotTop, Height: plotHeight, Line: sc.path(points, step)}
	c.Area = c.Line + "V1000H0Z"
	for v := sc.hi; v >= sc.lo; v -= unit {
		c.YTicks = append(c.YTicks, chartTick{Pos: sc.y(v), Pct: pct(sc.y(v)), Label: money.FormatEuros(v)})
	}
	c.XTicks = monthTicks(points[0].Date, points[len(points)-1].Date, sc.x)
	return c, sc
}

// path trace une série de soldes datés dans le repère : en escalier ou en
// ligne brisée.
func (s chartScale) path(points []forecast.Point, step bool) string {
	var b strings.Builder
	b.WriteString("M" + coord(s.x(points[0].Date)) + " " + coord(s.y(points[0].Balance)))
	for _, p := range points[1:] {
		if step {
			b.WriteString("H" + coord(s.x(p.Date)) + "V" + coord(s.y(p.Balance)))
		} else {
			b.WriteString("L" + coord(s.x(p.Date)) + " " + coord(s.y(p.Balance)))
		}
	}
	return b.String()
}

// newBalanceChart trace le solde prévisionnel jour par jour, avec le seuil,
// les périodes sous le seuil et le point bas ; ref, facultatif, est le
// prévisionnel de référence d'un scénario, tracé en pointillés.
func newBalanceChart(r forecast.Result, ref *forecast.Result) balanceChart {
	include := []int64{r.Threshold}
	if ref != nil {
		high := ref.Opening
		for _, p := range ref.Days {
			high = max(high, p.Balance)
		}
		include = append(include, ref.Low.Balance, high)
	}
	c, sc := lineChart("courbe", r.Days, true, include...)
	if ref != nil {
		c.Reference = sc.path(ref.Days, true)
	}
	c.Desc = "Point bas de " + money.Format(r.Low.Balance) + " le " + r.Low.Date.Format() + ". Le détail des échéances suit le graphique."
	c.HasThreshold, c.Threshold = true, sc.y(r.Threshold)
	halfDay := plotSize / sc.days / 2
	for _, rg := range r.Below {
		x0, x1 := max(sc.x(rg.From)-halfDay, 0), min(sc.x(rg.To)+halfDay, plotSize)
		c.Below = append(c.Below, chartBand{X: x0, W: x1 - x0})
	}
	c.HasLow, c.LowX, c.LowY = true, sc.x(r.Low.Date)/10, pct(sc.y(r.Low.Balance))
	return c
}

// monthTicks gradue l'axe des dates au premier de chaque mois, en sautant
// des mois pour ne pas dépasser maxXTicks étiquettes.
func monthTicks(from, to calendar.Date, x func(calendar.Date) float64) []chartTick {
	t := from.Time()
	first := t.AddDate(0, 0, 1-t.Day()).AddDate(0, 1, 0)
	months := 0
	for d := first; calendar.FromTime(d) <= to; d = d.AddDate(0, 1, 0) {
		months++
	}
	every := max((months+maxXTicks-1)/maxXTicks, 1)
	var ticks []chartTick
	for i := 0; i < months; i += every {
		d := calendar.FromTime(first.AddDate(0, i, 0))
		label := d.MonthShort()
		if d.Time().Month() == 1 {
			label += " " + strconv.Itoa(d.Time().Year())
		}
		pos := x(d)
		ticks = append(ticks, chartTick{Pos: pos, Pct: pos / 10, Label: label})
	}
	return ticks
}

// niceStep choisit un pas de graduation rond (1, 2 ou 5 × 10ⁿ euros)
// découpant l'étendue en quatre à six intervalles environ.
func niceStep(span int64) int64 {
	raw := math.Max(float64(span)/5, 100)
	mag := math.Pow(10, math.Floor(math.Log10(raw)))
	for _, m := range []float64{1, 2, 5} {
		if m*mag >= raw {
			return int64(m * mag)
		}
	}
	return int64(10 * mag)
}

// floorTo arrondit v au multiple de step inférieur.
func floorTo(v, step int64) int64 {
	q := v / step
	if v%step != 0 && v < 0 {
		q--
	}
	return q * step
}

// pct convertit une ordonnée du repère en pourcentage de hauteur du cadre.
func pct(y float64) float64 { return plotTop + plotHeight*y/plotSize }

func coord(v float64) string { return strconv.FormatFloat(v, 'f', 1, 64) }

// barChart compare des montants mensuels à une référence (le total des
// enveloppes) : une barre par mois, une ligne pour la référence.
type barChart struct {
	ID          string
	Desc        string
	Top, Height float64
	Bars        []chartBar
	Reference   float64 // ordonnée de la ligne de référence, repère 1000
	YTicks      []chartTick
	XTicks      []chartTick
}

// chartBar est une barre, repère 1000 ; Over marque un dépassement.
type chartBar struct {
	X, Y, W, H float64
	Over       bool
}

// newBarChart trace les dépenses mensuelles face au total des enveloppes.
func newBarChart(id string, months []forecast.Point, reference int64) barChart {
	hi := reference
	for _, m := range months {
		hi = max(hi, m.Balance)
	}
	unit := niceStep(hi)
	top := max(-floorTo(-hi, unit), unit)
	y := func(v int64) float64 { return plotSize * float64(top-max(v, 0)) / float64(top) }
	c := barChart{ID: id, Top: plotTop, Height: plotHeight, Reference: y(reference)}
	slot := plotSize / float64(len(months))
	for i, m := range months {
		x := slot*float64(i) + slot*0.15
		c.Bars = append(c.Bars, chartBar{X: x, W: slot * 0.7, Y: y(m.Balance), H: plotSize - y(m.Balance), Over: m.Balance > reference})
		if (len(months)-1-i)%2 == 0 || len(months) <= 6 { // au-delà de six mois, une étiquette sur deux, dont le mois en cours
			label := m.Date.MonthShort()
			if m.Date.Time().Month() == 1 {
				label += " " + strconv.Itoa(m.Date.Time().Year())
			}
			pos := slot*float64(i) + slot/2
			c.XTicks = append(c.XTicks, chartTick{Pos: pos, Pct: pos / 10, Label: label})
		}
	}
	for v := top; v >= 0; v -= unit {
		c.YTicks = append(c.YTicks, chartTick{Pos: y(v), Pct: pct(y(v)), Label: money.FormatEuros(v)})
	}
	return c
}
