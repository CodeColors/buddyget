package recurring

import (
	"slices"
	"testing"

	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/platform/calendar"
)

func TestSubscriptions(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	cats, _ := e.ledger.Categories(ctx)
	cat := func(name string) int64 {
		return cats[slices.IndexFunc(cats, func(c ledger.Category) bool { return c.FullName() == name })].ID
	}
	abonnements, sorties := cat("Loisirs › Abonnements"), cat("Loisirs › Sorties")
	for _, in := range []RuleInput{
		{Label: "PRLV SEPA NETFLIX.COM", Amount: -17_99, CategoryID: abonnements, Subscription: true},
		{Label: "Netflix Premium", Amount: -19_99, CategoryID: abonnements, Subscription: true},
		{Label: "Salle de sport", Amount: -29_90, CategoryID: sorties, Subscription: true, Start: "2026-01-05"},
		{Label: "Assurance smartphone", Amount: -120_00, Period: calendar.Yearly, Subscription: true, Start: "2026-03-01"},
		{Label: "Loyer", Amount: -700_00},
		{Label: "Journal", Amount: -10_00, Period: calendar.Weekly, Subscription: true, End: "2026-10-01"}, // terminé
	} {
		in.AccountID, in.Shift, in.Origin = e.account, calendar.NoShift, Manual
		if in.Period.Count == 0 {
			in.Period = calendar.Monthly
		}
		if in.Start == "" {
			in.Start = "2026-05-10"
		}
		if _, err := e.s.CreateRule(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.ledger.CreateTransaction(ctx, ledger.TransactionInput{AccountID: e.account, Date: "2026-10-02", Label: "SPOTIFY", Amount: -9_99, CategoryID: abonnements}); err != nil {
		t.Fatal(err)
	}

	subs, err := e.s.Subscriptions(ctx, "2026-10-31")
	if err != nil {
		t.Fatal(err)
	}
	type view struct {
		label   string
		monthly int64
		next    calendar.Date
		dup     []string
		unused  bool
	}
	var got []view
	for _, s := range subs {
		got = append(got, view{s.Label, s.Monthly, s.Next, s.Duplicates, s.Unused})
	}
	want := []view{
		{"Salle de sport", 29_90, "2026-11-05", nil, true}, // aucune autre sortie depuis 90 jours
		{"Netflix Premium", 19_99, "2026-11-10", []string{"PRLV SEPA NETFLIX.COM"}, false},
		{"PRLV SEPA NETFLIX.COM", 17_99, "2026-11-10", []string{"Netflix Premium"}, false},
		{"Assurance smartphone", 10_00, "2027-03-01", nil, false},
	}
	if !slices.EqualFunc(got, want, func(a, b view) bool {
		return a.label == b.label && a.monthly == b.monthly && a.next == b.next && slices.Equal(a.dup, b.dup) && a.unused == b.unused
	}) {
		t.Errorf("abonnements :\n%+v\nattendu :\n%+v", got, want)
	}
	if subs[3].Annual() != 120_00 {
		t.Errorf("coût annuel : %d", subs[3].Annual())
	}
	if m := MonthlyCost(Rule{Amount: -10_00, Period: calendar.Weekly}); m != 43_48 {
		t.Errorf("hebdomadaire ramené au mois : %d", m)
	}
	// Deux abonnements de même catégorie et de montant proche.
	close := []Subscription{
		{Rule: Rule{Label: "Deezer", CategoryID: 1}, Monthly: 10_99},
		{Rule: Rule{Label: "Qobuz", CategoryID: 1}, Monthly: 10_49},
		{Rule: Rule{Label: "Canal", CategoryID: 2}, Monthly: 10_99},
	}
	markDuplicates(close)
	if !slices.Equal(close[0].Duplicates, []string{"Qobuz"}) || close[2].Duplicates != nil {
		t.Errorf("doublons par montant : %+v", close)
	}
}
