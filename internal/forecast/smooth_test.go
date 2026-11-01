package forecast

import (
	"fmt"
	"testing"

	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/platform/validation"
	"github.com/codecolors/buddyget/internal/recurring"
)

// insurance est une assurance annuelle de 1 000 € lissée, payée le 15 mars.
func insurance(smoothAccount int64) recurring.Rule {
	return recurring.Rule{ID: 7, Label: "Assurance", AccountID: 1, Amount: -1_000_00, Period: calendar.Yearly,
		Shift: calendar.NextBusinessDay, Start: "2027-03-15", Active: true, Smooth: true, SmoothAccountID: smoothAccount}
}

func sum(events []Event) (total int64) {
	for _, e := range events {
		total += e.Amount
	}
	return total
}

func TestProvisions(t *testing.T) {
	names := map[int64]string{1: "Courant"}
	plans := plansOf([]recurring.Rule{insurance(0)})
	events := provisions(plans, names, "2026-11-01", "2027-12-31", nil, false)
	// 1re échéance : 4 parts de 83,33 € ou 83,34 € (novembre à février) puis leur
	// utilisation ; 2e échéance : 10 parts (mars à décembre).
	if len(events) != 4+1+10 {
		t.Fatalf("%d mouvements : %+v", len(events), events)
	}
	first, use := events[0], events[4]
	if first.Date != "2026-11-15" || first.Amount != -83_34 || first.Kind != Provision || first.Transfer || first.Label != "Provision : Assurance" {
		t.Errorf("première provision : %+v", first)
	}
	if use.Date != "2027-03-15" || use.Amount != -sum(events[:4]) || use.Amount != 333_34 || use.Label != "Provision utilisée : Assurance" {
		t.Errorf("provision utilisée : %+v", use)
	}
	if events[5].Date != "2027-03-15" || sum(events[5:]) != -833_33 {
		t.Errorf("cycle suivant : %+v", events[5:])
	}

	// Un cycle complet totalise exactement l'échéance.
	full := provisions(plans, names, "2026-03-15", "2027-03-14", nil, false)
	if len(full) != 12 || sum(full) != -1_000_00 {
		t.Errorf("cycle complet : %d parts, %d", len(full), sum(full))
	}

	// Échéance écartée : ni provision, ni utilisation.
	excluded := map[occurrence]bool{{7, "2027-03-15"}: true}
	if got := provisions(plans, names, "2026-11-01", "2027-03-31", excluded, false); len(got) != 1 || got[0].Date != "2027-03-15" {
		t.Errorf("échéance écartée : %+v", got)
	}

	// Régime établi (reste à vivre) : l'échéance est entièrement couverte.
	march := provisions(plans, names, "2027-03-01", "2027-03-31", nil, true)
	if len(march) != 2 || sum(march) != 1_000_00-83_33 {
		t.Errorf("mois de l'échéance : %+v", march)
	}

	// Compte de lissage : virements, vus de chaque côté.
	plans = plansOf([]recurring.Rule{insurance(2)})
	if got := provisions(plans, names, "2026-11-01", "2026-11-30", nil, false); len(got) != 1 || !got[0].Transfer || got[0].Amount != -83_34 {
		t.Errorf("vers le compte de lissage : %+v", got)
	}
	got := provisions(plans, map[int64]string{2: "Livret"}, "2026-11-01", "2027-03-31", nil, false)
	if len(got) != 6 || got[0].Amount != 83_34 || got[0].AccountName != "Livret" || got[4].Amount != -333_34 {
		t.Errorf("sur le compte de lissage : %+v", got)
	}
	if got := provisions(plans, map[int64]string{1: "Courant", 2: "Livret"}, "2026-11-01", "2027-03-31", nil, false); got != nil {
		t.Errorf("les deux comptes couverts : sans effet, %+v", got)
	}

	// Récurrence non lissée, suspendue ou terminée : rien.
	for _, change := range []func(*recurring.Rule){
		func(r *recurring.Rule) { r.Smooth = false },
		func(r *recurring.Rule) { r.Active = false },
		func(r *recurring.Rule) { r.End = "2026-12-31" },
	} {
		r := insurance(0)
		change(&r)
		if got := provisions(plansOf([]recurring.Rule{r}), names, "2026-11-01", "2027-12-31", nil, false); got != nil {
			t.Errorf("sans lissage : %+v", got)
		}
	}
}

func TestSmoothingValidation(t *testing.T) {
	e := newEnv(t)
	current := e.account("Courant", ledger.AccountCurrent, 0)
	other := e.account("Joint", ledger.AccountCurrent, 0)
	livret := e.account("Livret", ledger.AccountSavings, 0)
	ok := recurring.RuleInput{Label: "Taxe foncière", AccountID: current, Amount: -900_00, Period: calendar.Yearly,
		Shift: calendar.NextBusinessDay, Start: "2027-10-15", Origin: recurring.Manual, Smooth: true, SmoothAccountID: livret}
	for _, bad := range []func(*recurring.RuleInput){
		func(in *recurring.RuleInput) { in.Period = calendar.Monthly },
		func(in *recurring.RuleInput) { in.Period = calendar.Period{Count: 8, Unit: calendar.Week} },
		func(in *recurring.RuleInput) { in.Amount = 900_00 },
		func(in *recurring.RuleInput) { in.ToAccountID = other },
		func(in *recurring.RuleInput) { in.SmoothAccountID = other },
	} {
		in := ok
		bad(&in)
		if _, err := e.recurring.CreateRule(t.Context(), in); err == nil {
			t.Errorf("lissage invalide accepté : %+v", in)
		} else if _, user := validation.Message(err); !user {
			t.Errorf("erreur de saisie attendue : %v", err)
		}
	}
	id := e.rule(ok)
	rule, err := e.recurring.Rule(t.Context(), id)
	if err != nil || !rule.Smooth || rule.SmoothAccountName != "Livret" || rule.SmoothMonths() != 12 {
		t.Errorf("récurrence lissée : %+v, %v", rule, err)
	}
	// Le lissage retiré, le compte de lissage n'est pas conservé.
	ok.Smooth = false
	if err := e.recurring.UpdateRule(t.Context(), id, ok); err != nil {
		t.Fatal(err)
	}
	if rule, _ := e.recurring.Rule(t.Context(), id); rule.Smooth || rule.SmoothAccountID != 0 || rule.SmoothMonths() != 0 {
		t.Errorf("lissage retiré : %+v", rule)
	}
}

func TestForecastAndOutlookSmoothing(t *testing.T) {
	e := newEnv(t)
	current := e.account("Courant", ledger.AccountCurrent, 2_000_00)
	e.rule(recurring.RuleInput{Label: "Assurance habitation", AccountID: current, Amount: -240_00, Period: calendar.Yearly,
		Start: "2026-12-10", Smooth: true})

	// Prévisionnel : une part en novembre, utilisée avec l'échéance ; le
	// cycle suivant commence le même jour (provisions à date théorique).
	r := e.forecast(Query{Target: "2027-01-31"})
	want := Totals{Recurring: -240_00, Provision: -40_00}
	if r.Totals != want || r.Closing != 2_000_00-240_00-40_00 {
		t.Errorf("totaux : %+v, solde prévu %d", r.Totals, r.Closing)
	}
	var labels []string
	for _, l := range r.Lines {
		labels = append(labels, string(l.Date)+" "+l.Label)
	}
	wantLabels := "[2026-11-10 Provision : Assurance habitation 2026-12-10 Provision utilisée : Assurance habitation " +
		"2026-12-10 Provision : Assurance habitation 2026-12-10 Assurance habitation 2027-01-10 Provision : Assurance habitation]"
	if got := fmt.Sprint(labels); got != wantLabels {
		t.Errorf("lignes : %s", got)
	}

	// Reste à vivre de décembre : la provision du mois, pas l'échéance.
	o, _, err := e.s.Outlook(t.Context(), "2026-12-01")
	if err != nil || o.Upcoming+o.Provisions != -20_00 {
		t.Errorf("reste à vivre : %+v, %v", o, err)
	}
	// Une fois l'échéance payée, elle reste couverte par la provision.
	e.tx(ledger.TransactionInput{AccountID: current, Date: "2026-12-10", Label: "Assurance habitation", Amount: -240_00})
	if o, _, _ = e.s.Outlook(t.Context(), "2026-12-20"); o.Spent+o.Provisions != -20_00 {
		t.Errorf("après l'échéance : %+v", o)
	}
}
