package recurring

import (
	"fmt"
	"testing"

	"github.com/codecolors/buddyget/internal/platform/calendar"
)

const today calendar.Date = "2026-10-31"

// monthly produit n opérations mensuelles finissant au plus tard en octobre
// 2026, le jour day décalé de jitter[i] jours, au montant amounts[i % len].
func monthly(n, day int, jitter []int, label string, amounts ...int64) []Op {
	var ops []Op
	for i := range n {
		d := calendar.Monthly.Nth(calendar.Date(fmt.Sprintf("2026-%02d-%02d", 10, day)), i-n+1, false)
		if jitter != nil {
			d = d.AddDays(jitter[i%len(jitter)])
		}
		ops = append(ops, Op{AccountID: 1, Date: d, Label: fmt.Sprintf(label, i), Amount: amounts[i%len(amounts)], CategoryID: 7})
	}
	return ops
}

func find(cs []Candidate, label string) (Candidate, bool) {
	for _, c := range cs {
		if c.Label == label {
			return c, true
		}
	}
	return Candidate{}, false
}

func TestDetectMonthlyFixed(t *testing.T) {
	ops := monthly(12, 5, []int{0, 2, -1, 3, 0, -2}, "PRLV SEPA EDF ECH/%02d1026", -78_32)
	cs := Detect(ops, today)
	if len(cs) != 1 {
		t.Fatalf("%d séries, attendu 1 : %+v", len(cs), cs)
	}
	c := cs[0]
	if c.Period != calendar.Monthly || c.Amount != -78_32 || c.Estimated || c.LastDay || c.Occurrences != 12 || c.CategoryID != 7 {
		t.Errorf("série : %+v", c)
	}
	// Dernière occurrence : 05/10 décalé de jitter[11 % 6] = −2 jours.
	if c.Last != "2026-10-03" || c.Next != "2026-11-03" || c.Score != 100 {
		t.Errorf("dernière %s, prochaine %s, score %d", c.Last, c.Next, c.Score)
	}
	if c.Signature != "1|-|prlv sepa edf ech" {
		t.Errorf("signature %q : références et dates doivent être ignorées", c.Signature)
	}
}

func TestDetectPeriodsAndAmounts(t *testing.T) {
	var ops []Op
	// Salaire versé le dernier jour du mois.
	for i := range 6 {
		d := calendar.Monthly.Nth("2026-05-31", i-1, true)
		ops = append(ops, Op{AccountID: 1, Date: d, Label: "VIR SALAIRE", Amount: 2450_00})
	}
	// Eau, trimestrielle, montant variable (±8 %).
	for i, a := range []int64{-61_20, -64_80, -59_90, -66_10} {
		ops = append(ops, Op{AccountID: 1, Date: calendar.Quarterly.Nth("2025-10-15", i, false), Label: "PRLV EAU", Amount: a})
	}
	// Marché le samedi.
	for i := range 8 {
		ops = append(ops, Op{AccountID: 1, Date: calendar.Weekly.Nth("2026-09-05", i, false), Label: "MARCHE", Amount: -25_00})
	}
	// Assurance annuelle.
	for i := range 3 {
		ops = append(ops, Op{AccountID: 2, Date: calendar.Yearly.Nth("2024-03-02", i, false), Label: "ASSURANCE HABITATION", Amount: -310_00})
	}
	cs := Detect(ops, today)

	for _, w := range []struct {
		label     string
		period    calendar.Period
		amount    int64
		estimated bool
		lastDay   bool
		next      calendar.Date
	}{
		{"VIR SALAIRE", calendar.Monthly, 2450_00, false, true, "2026-10-31"}, // attendu aujourd'hui
		{"PRLV EAU", calendar.Quarterly, -63_60, true, false, "2027-01-15"},   // octobre manqué : 16 jours de retard
		{"MARCHE", calendar.Weekly, -25_00, false, false, "2026-10-31"},
		{"ASSURANCE HABITATION", calendar.Yearly, -310_00, false, false, "2027-03-02"},
	} {
		c, ok := find(cs, w.label)
		if !ok {
			t.Errorf("%s non détecté", w.label)
			continue
		}
		if c.Period != w.period || c.Amount != w.amount || c.Estimated != w.estimated || c.LastDay != w.lastDay || c.Next != w.next {
			t.Errorf("%s : %+v", w.label, c)
		}
	}
	if c, _ := find(cs, "PRLV EAU"); c.Score >= 100 || c.Score < minScore {
		t.Errorf("un montant variable réduit la confiance : %d", c.Score)
	}
	// Une échéance prévue le 29/10 et pas encore passée reste attendue.
	late := Detect(monthly(6, 29, nil, "LOYER", -700_00), today)
	if len(late) != 1 || late[0].Last != "2026-10-29" || late[0].Next != "2026-11-29" {
		t.Errorf("loyer à jour : %+v", late)
	}
	delayed := Detect(monthly(6, 29, nil, "LOYER", -700_00)[:5], today)
	if len(delayed) != 1 || delayed[0].Next != "2026-10-29" {
		t.Errorf("échéance du 29/10 en retard de deux jours : %+v", delayed)
	}
	if c, _ := find(cs, "ASSURANCE HABITATION"); c.Score != 50 {
		t.Errorf("trois occurrences seulement : confiance %d, attendu 50", c.Score)
	}
}

func TestDetectRejects(t *testing.T) {
	var ops []Op
	// Dates irrégulières.
	for _, d := range []calendar.Date{"2026-01-03", "2026-01-29", "2026-03-15", "2026-04-02", "2026-06-20"} {
		ops = append(ops, Op{AccountID: 1, Date: d, Label: "RESTAURANT", Amount: -30_00})
	}
	// Série arrêtée depuis quatre mois.
	ops = append(ops, monthly(8, 10, nil, "ABONNEMENT SALLE %d", -29_90)...)
	for i := range ops {
		if ops[i].Label != "RESTAURANT" {
			ops[i].Date = ops[i].Date.AddDays(-122)
		}
	}
	// Deux occurrences seulement.
	ops = append(ops, Op{AccountID: 1, Date: "2026-09-01", Label: "CANTINE", Amount: -40_00}, Op{AccountID: 1, Date: "2026-10-01", Label: "CANTINE", Amount: -40_00})
	if cs := Detect(ops, today); len(cs) != 0 {
		t.Errorf("aucune série attendue : %+v", cs)
	}
}

func TestDetectSplitsAmountsAndDirections(t *testing.T) {
	// Hausse de prix au-delà de 10 % : seule la série récente est retenue.
	var ops []Op
	for i := range 10 {
		amount := int64(-13_49)
		if i >= 6 {
			amount = -17_99
		}
		ops = append(ops, Op{AccountID: 1, Date: calendar.Monthly.Nth("2026-01-12", i, false), Label: "NETFLIX.COM", Amount: amount})
	}
	// Même libellé en entrée et en sortie : deux groupes distincts.
	for i := range 4 {
		ops = append(ops, Op{AccountID: 1, Date: calendar.Monthly.Nth("2026-07-20", i, false), Label: "TONTINE", Amount: -50_00})
		ops = append(ops, Op{AccountID: 1, Date: calendar.Monthly.Nth("2026-07-25", i, false), Label: "TONTINE", Amount: 50_00})
	}
	cs := Detect(ops, today)
	netflix, ok := find(cs, "NETFLIX.COM")
	if !ok || netflix.Amount != -17_99 || netflix.Occurrences != 4 || netflix.First != "2026-07-12" {
		t.Errorf("Netflix : %+v", netflix)
	}
	signs := 0
	for _, c := range cs {
		if c.Label == "TONTINE" {
			signs++
		}
	}
	if signs != 2 {
		t.Errorf("%d séries « TONTINE », attendu une par sens", signs)
	}
}
