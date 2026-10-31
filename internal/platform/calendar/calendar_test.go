package calendar

import (
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	for in, ok := range map[string]bool{
		"2026-10-31": true,
		"2028-02-29": true, // bissextile
		"2026-02-29": false,
		"31/10/2026": false,
		"2026-1-5":   false,
		"":           false,
	} {
		d, err := Parse(in)
		if (err == nil) != ok || (ok && string(d) != in) {
			t.Errorf("Parse(%q) = %q, %v", in, d, err)
		}
	}
}

func TestFormatAndFromTime(t *testing.T) {
	paris, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		t.Fatal(err)
	}
	// 23h30 UTC le 31/12 : déjà le 1er janvier à Paris.
	d := FromTime(time.Date(2026, 12, 31, 23, 30, 0, 0, time.UTC).In(paris))
	if d != "2027-01-01" || d.Format() != "01/01/2027" {
		t.Errorf("FromTime = %q (%s)", d, d.Format())
	}
	if Date("").Format() != "" {
		t.Error("une date vide s'affiche vide")
	}
}

func TestMonthLabels(t *testing.T) {
	if got := Date("2026-11-02").MonthLabel(); got != "novembre 2026" {
		t.Errorf("MonthLabel = %q", got)
	}
	if got := Date("2027-02-28").MonthShort(); got != "févr." {
		t.Errorf("MonthShort = %q", got)
	}
}
