package calendar

import "time"

// isBusinessDay indique un jour ouvré : ni samedi, ni dimanche, ni jour
// férié légal en France métropolitaine.
func isBusinessDay(t time.Time) bool {
	if wd := t.Weekday(); wd == time.Saturday || wd == time.Sunday {
		return false
	}
	return !isHoliday(t)
}

// isHoliday reconnaît les onze jours fériés légaux (Code du travail,
// art. L3133-1) : huit à date fixe, trois calés sur Pâques. Les jours
// propres à l'Alsace-Moselle ne sont pas comptés.
func isHoliday(t time.Time) bool {
	switch m, day := t.Month(), t.Day(); {
	case m == time.January && day == 1,
		m == time.May && (day == 1 || day == 8),
		m == time.July && day == 14,
		m == time.August && day == 15,
		m == time.November && (day == 1 || day == 11),
		m == time.December && day == 25:
		return true
	}
	switch int(t.Sub(easter(t.Year())).Hours() / 24) {
	case 1, 39, 50: // lundi de Pâques, Ascension, lundi de Pentecôte
		return true
	}
	return false
}

// easter renvoie le dimanche de Pâques du calendrier grégorien
// (algorithme anonyme dit de Meeus/Jones/Butcher).
func easter(year int) time.Time {
	a, b, c := year%19, year/100, year%100
	d, e := b/4, b%4
	f := (b + 8) / 25
	g := (b - f + 1) / 3
	h := (19*a + b - d - g + 15) % 30
	i, k := c/4, c%4
	l := (32 + 2*e + 2*i - h - k) % 7
	m := (a + 11*h + 22*l) / 451
	month := (h + l - 7*m + 114) / 31
	day := (h+l-7*m+114)%31 + 1
	return time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
}

// Shift dit comment reporter une échéance qui tombe un jour non ouvré.
type Shift string

const (
	NextBusinessDay     Shift = "suivant"
	PreviousBusinessDay Shift = "precedent"
	NoShift             Shift = "aucun"
)

// MaxShift majore, en jours, le report d'une échéance au jour ouvré voisin
// (trois jours au plus en pratique : week-end prolongé d'un jour férié).
const MaxShift = 7

// Shifts liste les règles de report dans l'ordre d'affichage.
var Shifts = []Shift{NextBusinessDay, PreviousBusinessDay, NoShift}

// Label décrit la règle de report.
func (s Shift) Label() string {
	switch s {
	case NextBusinessDay:
		return "Jour ouvré suivant"
	case PreviousBusinessDay:
		return "Jour ouvré précédent"
	}
	return "Pas de report"
}

// Apply renvoie la date effective d'une échéance prévue le jour d.
func (s Shift) Apply(d Date) Date {
	step := 0
	switch s {
	case NextBusinessDay:
		step = 1
	case PreviousBusinessDay:
		step = -1
	default:
		return d
	}
	t := d.Time()
	for !isBusinessDay(t) {
		t = t.AddDate(0, 0, step)
	}
	return FromTime(t)
}
