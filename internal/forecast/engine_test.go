package forecast

import (
	"slices"
	"testing"

	"github.com/codecolors/buddyget/internal/platform/calendar"
)

func TestComputeBalancesAndTotals(t *testing.T) {
	res := Compute(Input{
		Today: "2026-10-31", Target: "2026-11-30", Opening: 500_00, Threshold: 0,
		Events: []Event{
			{Date: "2026-11-05", Label: "Loyer", Amount: -700_00, Kind: Recurring},
			{Date: "2026-11-28", Label: "Salaire", Amount: 2000_00, Kind: Recurring},
			{Date: "2026-11-05", Label: "Prime", Amount: 100_00, Kind: Planned},
			{Date: "2026-11-10", Label: "Dentiste", Amount: -60_00, Kind: Planned},
			{Date: "2026-11-15", Label: "Abonnement", Amount: -10_00, Kind: Recurring, Excluded: true},
			{Date: "2026-10-31", Label: "Déjà passé", Amount: -1, Kind: Planned},
			{Date: "2026-12-01", Label: "Après la cible", Amount: -1, Kind: Planned},
		},
	})
	if len(res.Days) != 31 || res.Days[0] != (Point{"2026-10-31", 500_00}) || res.Days[30].Date != "2026-11-30" {
		t.Fatalf("jours : %d, du %+v au %+v", len(res.Days), res.Days[0], res.Days[len(res.Days)-1])
	}
	want := Totals{Income: 2100_00, Recurring: -700_00, Other: -60_00}
	if res.Totals != want || res.Closing != 1840_00 {
		t.Errorf("totaux : %+v, solde final %d", res.Totals, res.Closing)
	}
	if got := res.Opening + res.Totals.Income + res.Totals.Recurring + res.Totals.Other + res.Totals.Variable; got != res.Closing {
		t.Errorf("les totaux n'expliquent pas le solde final : %d ≠ %d", got, res.Closing)
	}
	// Le même jour, le revenu passe avant la dépense ; l'échéance écartée
	// reste listée sans peser sur le solde.
	labels := make([]string, len(res.Lines))
	for i, l := range res.Lines {
		labels[i] = l.Label
	}
	if !slices.Equal(labels, []string{"Prime", "Loyer", "Dentiste", "Abonnement", "Salaire"}) {
		t.Errorf("ordre des lignes : %v", labels)
	}
	if res.Lines[1].Balance != -100_00 || res.Lines[3].Balance != -160_00 || res.Lines[4].Balance != 1840_00 {
		t.Errorf("soldes cumulés : %+v", res.Lines)
	}
	if res.Low != (Point{"2026-11-10", -160_00}) {
		t.Errorf("point bas : %+v", res.Low)
	}
	if !slices.Equal(res.Below, []Range{{From: "2026-11-05", To: "2026-11-27", Low: -160_00}}) {
		t.Errorf("périodes sous le seuil : %+v", res.Below)
	}
}

func TestComputeThresholdRanges(t *testing.T) {
	res := Compute(Input{
		Today: "2026-01-01", Target: "2026-01-10", Opening: 50_00, Threshold: 100_00,
		Events: []Event{
			{Date: "2026-01-03", Amount: 100_00},
			{Date: "2026-01-06", Amount: -80_00},
			{Date: "2026-01-08", Amount: -10_00},
		},
	})
	want := []Range{{From: "2026-01-01", To: "2026-01-02", Low: 50_00}, {From: "2026-01-06", To: "2026-01-10", Low: 60_00}}
	if !slices.Equal(res.Below, want) {
		t.Errorf("périodes sous le seuil : %+v", res.Below)
	}
	if res.Low != (Point{"2026-01-01", 50_00}) {
		t.Errorf("le point bas peut être aujourd'hui : %+v", res.Low)
	}
}

func TestComputeVariableSpreadByMonthLength(t *testing.T) {
	// 310 € par mois : 10 € par jour en janvier, 310/28 en février 2026,
	// 310/29 en février 2028 (bissextile).
	for _, c := range []struct {
		today, target calendar.Date
		want          int64
	}{
		{"2025-12-31", "2026-01-31", -310_00},
		{"2025-12-31", "2026-01-10", -100_00},
		{"2026-01-31", "2026-02-28", -310_00},
		{"2028-01-31", "2028-02-29", -310_00},
		{"2028-01-31", "2028-02-28", -299_31},
		{"2026-01-15", "2026-03-15", -310_00 - 160_00 - 310_00*15/31},
	} {
		res := Compute(Input{Today: c.today, Target: c.target, Variable: -310_00})
		if res.Totals.Variable != c.want || res.Closing != c.want {
			t.Errorf("du %s au %s : %d (solde %d), attendu %d", c.today, c.target, res.Totals.Variable, res.Closing, c.want)
		}
	}
}

func TestComputeVariableInLines(t *testing.T) {
	res := Compute(Input{
		Today: "2026-03-31", Target: "2026-04-30", Opening: 1000_00, Variable: -300_00,
		Events: []Event{{Date: "2026-04-10", Label: "Assurance", Amount: -50_00, Kind: Recurring}},
	})
	// Dix jours de dépenses variables (10 € par jour) précèdent l'échéance.
	if len(res.Lines) != 1 || res.Lines[0].Balance != 850_00 || res.Closing != 650_00 {
		t.Errorf("ligne : %+v, solde final %d", res.Lines, res.Closing)
	}
	if res.Days[10] != (Point{"2026-04-10", 850_00}) {
		t.Errorf("solde du 10 avril : %+v", res.Days[10])
	}
}

func TestComputeSameDayTarget(t *testing.T) {
	res := Compute(Input{Today: "2026-10-31", Target: "2026-10-31", Opening: 10_00})
	if len(res.Days) != 1 || res.Closing != 10_00 || res.Low.Balance != 10_00 || len(res.Below) != 0 {
		t.Errorf("sans horizon : %+v", res)
	}
}
