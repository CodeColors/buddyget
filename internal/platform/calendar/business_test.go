package calendar

import "testing"

func TestEaster(t *testing.T) {
	for year, want := range map[int]Date{
		2024: "2024-03-31", 2025: "2025-04-20", 2026: "2026-04-05", 2027: "2027-03-28", 2038: "2038-04-25",
	} {
		if got := FromTime(easter(year)); got != want {
			t.Errorf("Pâques %d = %s, attendu %s", year, got, want)
		}
	}
}

func TestIsBusinessDay(t *testing.T) {
	for d, want := range map[Date]bool{
		"2026-11-02": true,  // lundi
		"2026-10-31": false, // samedi
		"2026-11-01": false, // dimanche (et Toussaint)
		"2026-11-11": false, // Armistice, un mercredi
		"2026-04-06": false, // lundi de Pâques
		"2026-05-14": false, // Ascension
		"2026-05-25": false, // lundi de Pentecôte
		"2026-05-08": false, // Victoire 1945
		"2026-07-14": false,
		"2026-12-25": false,
		"2026-12-24": true,
		"2026-12-26": false, // samedi
		"2026-04-03": true,  // vendredi saint : férié en Alsace-Moselle seulement
	} {
		if got := isBusinessDay(d.Time()); got != want {
			t.Errorf("%s ouvré = %v, attendu %v", d, got, want)
		}
	}
}

func TestShiftApply(t *testing.T) {
	cases := []struct {
		shift Shift
		in    Date
		want  Date
	}{
		{NextBusinessDay, "2026-11-02", "2026-11-02"},
		{NextBusinessDay, "2026-10-31", "2026-11-02"}, // samedi, puis dimanche férié
		{PreviousBusinessDay, "2026-10-31", "2026-10-30"},
		{NextBusinessDay, "2026-04-04", "2026-04-07"}, // samedi avant le lundi de Pâques
		{PreviousBusinessDay, "2026-04-06", "2026-04-03"},
		{NextBusinessDay, "2026-12-25", "2026-12-28"}, // Noël un vendredi
		{PreviousBusinessDay, "2027-01-01", "2026-12-31"},
		{NoShift, "2026-10-31", "2026-10-31"},
	}
	for _, c := range cases {
		if got := c.shift.Apply(c.in); got != c.want {
			t.Errorf("%s(%s) = %s, attendu %s", c.shift, c.in, got, c.want)
		}
	}
}
