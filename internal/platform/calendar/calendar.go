// Package calendar manipule les dates civiles (AAAA-MM-JJ, sans heure ni
// fuseau), format de stockage de toutes les dates métier.
package calendar

import (
	"strconv"
	"time"

	"github.com/codecolors/buddyget/internal/platform/validation"
)

const layout = "2006-01-02"

// Date est une date civile au format AAAA-MM-JJ ; l'ordre lexical est
// l'ordre chronologique.
type Date string

// Parse valide une date au format AAAA-MM-JJ (champ <input type="date">).
func Parse(s string) (Date, error) {
	t, err := time.Parse(layout, s)
	if err != nil {
		return "", validation.Error("Date invalide.")
	}
	return FromTime(t), nil
}

// FromTime renvoie la date civile d'un instant, dans son fuseau.
func FromTime(t time.Time) Date { return Date(t.Format(layout)) }

// Today renvoie la date du jour dans le fuseau local (variable TZ).
func Today() Date { return FromTime(time.Now()) }

// Format affiche la date au format JJ/MM/AAAA.
func (d Date) Format() string {
	if len(d) != len(layout) {
		return string(d)
	}
	return string(d[8:10] + "/" + d[5:7] + "/" + d[0:4])
}

// Time renvoie la date à minuit UTC, pour les calculs.
func (d Date) Time() time.Time {
	t, _ := time.Parse(layout, string(d))
	return t
}

// AddDays décale la date d'un nombre de jours (négatif pour reculer).
func (d Date) AddDays(n int) Date { return FromTime(d.Time().AddDate(0, 0, n)) }

// DaysUntil renvoie le nombre de jours de d à other (négatif si other est
// antérieure).
func (d Date) DaysUntil(other Date) int {
	return int(other.Time().Sub(d.Time()).Hours() / 24)
}

var months = [...]string{"janvier", "février", "mars", "avril", "mai", "juin", "juillet", "août", "septembre", "octobre", "novembre", "décembre"}

// Abréviations usuelles (Imprimerie nationale) ; les mois courts restent
// entiers.
var monthsShort = [...]string{"janv.", "févr.", "mars", "avr.", "mai", "juin", "juil.", "août", "sept.", "oct.", "nov.", "déc."}

// MonthLabel renvoie le mois et l'année : « novembre 2026 ».
func (d Date) MonthLabel() string {
	t := d.Time()
	return months[t.Month()-1] + " " + strconv.Itoa(t.Year())
}

// MonthShort renvoie le mois abrégé : « nov. ».
func (d Date) MonthShort() string { return monthsShort[d.Time().Month()-1] }
