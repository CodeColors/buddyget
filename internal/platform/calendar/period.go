package calendar

import (
	"fmt"
	"time"
)

// Unit est l'unité d'une périodicité.
type Unit string

const (
	Day   Unit = "jour"
	Week  Unit = "semaine"
	Month Unit = "mois"
)

// Period est une périodicité : tous les Count jours, semaines ou mois.
// Hebdomadaire, mensuelle, bimestrielle, trimestrielle, semestrielle,
// annuelle et personnalisée en sont des cas particuliers.
type Period struct {
	Count int
	Unit  Unit
}

// Périodicités usuelles, proposées à la saisie.
var (
	Weekly     = Period{1, Week}
	Monthly    = Period{1, Month}
	Bimonthly  = Period{2, Month}
	Quarterly  = Period{3, Month}
	Semiannual = Period{6, Month}
	Yearly     = Period{12, Month}
)

// Valid indique une périodicité utilisable (au plus deux ans).
func (p Period) Valid() bool {
	switch p.Unit {
	case Day:
		return p.Count >= 1 && p.Count <= 730
	case Week:
		return p.Count >= 1 && p.Count <= 104
	case Month:
		return p.Count >= 1 && p.Count <= 24
	}
	return false
}

// Label décrit la périodicité : « Chaque mois », « Tous les 2 mois »…
func (p Period) Label() string {
	switch p {
	case Weekly:
		return "Chaque semaine"
	case Monthly:
		return "Chaque mois"
	case Bimonthly:
		return "Tous les 2 mois"
	case Quarterly:
		return "Chaque trimestre"
	case Semiannual:
		return "Chaque semestre"
	case Yearly:
		return "Chaque année"
	}
	switch p.Unit {
	case Day:
		if p.Count == 1 {
			return "Chaque jour"
		}
		return fmt.Sprintf("Tous les %d jours", p.Count)
	case Week:
		return fmt.Sprintf("Toutes les %d semaines", p.Count)
	}
	return fmt.Sprintf("Tous les %d mois", p.Count)
}

// Days renvoie la durée moyenne de la période en jours.
func (p Period) Days() float64 {
	switch p.Unit {
	case Day:
		return float64(p.Count)
	case Week:
		return float64(7 * p.Count)
	}
	return float64(p.Count) * 365.25 / 12
}

// Nth renvoie la n-ième échéance (0 : start) d'une série qui commence à
// start. Le calcul part toujours de start, sans dérive : une série du 31
// tombe le 28 ou le 29 février puis de nouveau le 31 mars. Avec lastDay,
// chaque échéance mensuelle est le dernier jour du mois.
func (p Period) Nth(start Date, n int, lastDay bool) Date {
	t := start.Time()
	switch p.Unit {
	case Day:
		return FromTime(t.AddDate(0, 0, n*p.Count))
	case Week:
		return FromTime(t.AddDate(0, 0, 7*n*p.Count))
	}
	first := time.Date(t.Year(), t.Month()+time.Month(n*p.Count), 1, 0, 0, 0, 0, time.UTC)
	last := first.AddDate(0, 1, -1).Day()
	day := t.Day()
	if lastDay || day > last {
		day = last
	}
	return FromTime(first.AddDate(0, 0, day-1))
}

// IsLastDayOfMonth indique que la date est le dernier jour de son mois.
func (d Date) IsLastDayOfMonth() bool {
	return d.Time().AddDate(0, 0, 1).Day() == 1
}

// NextOnOrAfter renvoie la première échéance postérieure ou égale à from,
// ainsi que son rang.
func (p Period) NextOnOrAfter(start Date, lastDay bool, from Date) (Date, int) {
	if from <= start {
		return start, 0
	}
	// Estimation du rang, corrigée ensuite : l'écart est d'une période au plus.
	n := max(int(float64(start.DaysUntil(from))/p.Days())-1, 0)
	for {
		if d := p.Nth(start, n, lastDay); d >= from {
			return d, n
		}
		n++
	}
}
