// Package calendar manipule les dates civiles (AAAA-MM-JJ, sans heure ni
// fuseau), format de stockage de toutes les dates métier.
package calendar

import (
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
