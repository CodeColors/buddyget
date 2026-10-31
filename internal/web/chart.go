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

// balanceChart est la courbe du solde jour par jour.
type balanceChart struct {
	Top, Height float64 // cadre du tracé, en %
	Line, Area  string  // tracés (attribut d), repère 1000 × 1000
	Threshold   float64
	Below       []chartBand
	LowX, LowY  float64 // point bas, en %
	YTicks      []chartTick
	XTicks      []chartTick
}

// chartBand est une bande verticale (période sous le seuil), repère 1000.
type chartBand struct{ X, W float64 }

// chartTick est une graduation : position dans le repère 1000 (Pos) et en %
// du cadre (Pct).
type chartTick struct {
	Pos, Pct float64
	Label    string
}

func newBalanceChart(r forecast.Result) balanceChart {
	lo, hi := min(r.Low.Balance, r.Threshold), r.Threshold
	for _, p := range r.Days {
		hi = max(hi, p.Balance)
	}
	step := niceStep(hi - lo)
	lo, hi = floorTo(lo, step), -floorTo(-hi, step)
	if hi == lo {
		hi = lo + step
	}
	n := float64(max(len(r.Days)-1, 1))
	x := func(i int) float64 { return plotSize * float64(i) / n }
	y := func(v int64) float64 { return plotSize * float64(hi-v) / float64(hi-lo) }

	c := balanceChart{Top: plotTop, Height: plotHeight}
	var b strings.Builder
	b.WriteString("M0 ")
	b.WriteString(coord(y(r.Days[0].Balance)))
	for i, p := range r.Days[1:] {
		b.WriteString("H" + coord(x(i+1)) + "V" + coord(y(p.Balance)))
	}
	c.Line = b.String()
	c.Area = c.Line + "V1000H0Z"
	c.Threshold = y(r.Threshold)

	for _, rg := range r.Below {
		from, to := r.Today.DaysUntil(rg.From), r.Today.DaysUntil(rg.To)
		x0, x1 := max(x(from)-x(1)/2, 0), min(x(to)+x(1)/2, plotSize)
		c.Below = append(c.Below, chartBand{X: x0, W: x1 - x0})
	}
	c.LowX = x(r.Today.DaysUntil(r.Low.Date)) / 10
	c.LowY = pct(y(r.Low.Balance))

	for v := hi; v >= lo; v -= step {
		c.YTicks = append(c.YTicks, chartTick{Pos: y(v), Pct: pct(y(v)), Label: money.FormatEuros(v)})
	}
	c.XTicks = monthTicks(r.Today, r.Target, x)
	return c
}

// monthTicks gradue l'axe des dates au premier de chaque mois, en sautant
// des mois pour ne pas dépasser maxXTicks étiquettes.
func monthTicks(today, target calendar.Date, x func(int) float64) []chartTick {
	t := today.Time()
	first := t.AddDate(0, 0, 1-t.Day()).AddDate(0, 1, 0)
	months := 0
	for d := first; calendar.FromTime(d) <= target; d = d.AddDate(0, 1, 0) {
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
		pos := x(today.DaysUntil(d))
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
