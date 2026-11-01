package calendar

import (
	"slices"
	"testing"
)

func series(p Period, start Date, lastDay bool, n int) []Date {
	out := make([]Date, n)
	for i := range n {
		out[i] = p.Nth(start, i, lastDay)
	}
	return out
}

func TestNthMonthEnds(t *testing.T) {
	for name, c := range map[string]struct {
		p       Period
		start   Date
		lastDay bool
		want    []Date
	}{
		"le 31, sans dérive": {Monthly, "2027-12-31", false,
			[]Date{"2027-12-31", "2028-01-31", "2028-02-29", "2028-03-31", "2028-04-30", "2028-05-31"}},
		"le 30, février non bissextile": {Monthly, "2026-01-30", false,
			[]Date{"2026-01-30", "2026-02-28", "2026-03-30"}},
		"dernier jour du mois": {Monthly, "2026-04-30", true,
			[]Date{"2026-04-30", "2026-05-31", "2026-06-30", "2026-07-31"}},
		"29 février annuel": {Yearly, "2028-02-29", false,
			[]Date{"2028-02-29", "2029-02-28", "2030-02-28", "2031-02-28", "2032-02-29"}},
		"trimestriel à cheval sur l'année": {Quarterly, "2026-11-15", false,
			[]Date{"2026-11-15", "2027-02-15", "2027-05-15"}},
		"hebdomadaire": {Weekly, "2026-12-28", false,
			[]Date{"2026-12-28", "2027-01-04", "2027-01-11"}},
		"tous les 10 jours": {Period{10, Day}, "2026-02-25", false,
			[]Date{"2026-02-25", "2026-03-07", "2026-03-17"}},
	} {
		t.Run(name, func(t *testing.T) {
			if got := series(c.p, c.start, c.lastDay, len(c.want)); !slices.Equal(got, c.want) {
				t.Errorf("échéances %v, attendu %v", got, c.want)
			}
		})
	}
}

func TestNextOnOrAfter(t *testing.T) {
	for _, c := range []struct {
		p     Period
		start Date
		from  Date
		want  Date
		rank  int
	}{
		{Monthly, "2026-01-31", "2026-01-01", "2026-01-31", 0},
		{Monthly, "2026-01-31", "2026-03-01", "2026-03-31", 2},
		{Monthly, "2026-01-31", "2026-02-28", "2026-02-28", 1},
		{Monthly, "2016-01-05", "2026-10-31", "2026-11-05", 130},
		{Yearly, "2028-02-29", "2029-03-01", "2030-02-28", 2},
		{Period{2, Week}, "2026-10-01", "2026-10-16", "2026-10-29", 2},
	} {
		got, rank := c.p.NextOnOrAfter(c.start, false, c.from)
		if got != c.want || rank != c.rank {
			t.Errorf("%v depuis %s, à partir du %s : %s (rang %d), attendu %s (rang %d)", c.p, c.start, c.from, got, rank, c.want, c.rank)
		}
	}
}

func TestPeriodLabelAndValidity(t *testing.T) {
	for p, want := range map[Period]string{
		Weekly: "Chaque semaine", Quarterly: "Chaque trimestre", Yearly: "Chaque année",
		{4, Month}: "Tous les 4 mois", {3, Week}: "Toutes les 3 semaines", {1, Day}: "Chaque jour", {15, Day}: "Tous les 15 jours",
	} {
		if got := p.Label(); got != want {
			t.Errorf("Label(%v) = %q, attendu %q", p, got, want)
		}
	}
	for p, ok := range map[Period]bool{Monthly: true, {0, Month}: false, {25, Month}: false, {1, "an"}: false, {730, Day}: true} {
		if p.Valid() != ok {
			t.Errorf("Valid(%v) = %v", p, !ok)
		}
	}
}

func TestDateArithmetic(t *testing.T) {
	if d := Date("2028-02-28").AddDays(1); d != "2028-02-29" {
		t.Errorf("AddDays : %s", d)
	}
	if n := Date("2026-10-01").DaysUntil("2026-09-28"); n != -3 {
		t.Errorf("DaysUntil : %d", n)
	}
	if !Date("2028-02-29").IsLastDayOfMonth() || Date("2027-02-27").IsLastDayOfMonth() {
		t.Error("IsLastDayOfMonth")
	}
}
