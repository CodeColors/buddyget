package forecast

import (
	"github.com/codecolors/buddyget/internal/platform/calendar"
)

// provisions renvoie les mouvements du lissage des récurrences actives
// lissées, datés de from à to inclus. Une échéance espacée de N mois est
// provisionnée en N parts mensuelles, du jour de l'échéance précédente au
// mois qui précède la suivante ; à l'échéance, la provision est utilisée et
// compense le paiement. Avec un compte d'épargne de lissage, provision et
// utilisation sont des virements vers et depuis ce compte ; sans compte,
// des mises de côté.
//
// Sans steady, la provision utilisée se limite aux parts datées de la
// période : celles d'avant from, non mises de côté dans le solde de départ,
// ne compensent rien. Avec steady (reste à vivre du mois), le lissage est
// supposé en régime établi : chaque échéance est entièrement provisionnée.
// Une échéance écartée ou supprimée par un scénario n'est ni provisionnée ni
// compensée ; une variation change la provision de son cycle.
func provisions(plans []plan, names map[int64]string, from, to calendar.Date, excluded map[occurrence]bool, steady bool) []Event {
	var out []Event
	for _, p := range plans {
		n := p.SmoothMonths()
		if n == 0 || !p.Active {
			continue
		}
		account, accountOK := names[p.AccountID]
		saving, savingOK := names[p.SmoothAccountID]
		if !accountOK && !savingOK || accountOK && savingOK {
			continue
		}
		var changed bool // cycle ajouté ou modifié par un scénario
		emit := func(date calendar.Date, label string, value int64) {
			e := Event{Date: date, Label: label, Kind: Provision, Estimated: p.Estimated, Transfer: p.SmoothAccountID != 0,
				Scenario: changed, Amount: value, AccountID: p.AccountID, AccountName: account}
			if !accountOK {
				e.Amount, e.AccountID, e.AccountName = -value, p.SmoothAccountID, saving
			}
			out = append(out, e)
		}
		// Comme pour les échéances, une date théorique un peu antérieure à
		// from peut être reportée dans la période.
		_, k := p.Period.NextOnOrAfter(p.Start, p.LastDay, from.AddDays(-calendar.MaxShift))
		for ; ; k++ {
			nominal := p.Period.Nth(p.Start, k, p.LastDay)
			first := calendar.Monthly.Nth(p.Start, (k-1)*n, p.LastDay)
			if p.End != "" && nominal > p.End || first > to {
				break
			}
			amount := p.amountAt(nominal)
			if amount == 0 || excluded[occurrence{p.ID, nominal}] {
				continue
			}
			changed = p.simulated || amount != p.Amount
			var provisioned int64
			for m := range n {
				date := calendar.Monthly.Nth(p.Start, (k-1)*n+m, p.LastDay)
				part := amount*int64(m+1)/int64(n) - amount*int64(m)/int64(n)
				if date < from || date > to {
					continue
				}
				provisioned += part
				emit(date, "Provision : "+p.Label, part)
			}
			if steady {
				provisioned = amount
			}
			if due := p.Shift.Apply(nominal); due >= from && due <= to && provisioned != 0 {
				emit(due, "Provision utilisée : "+p.Label, -provisioned)
			}
		}
	}
	return out
}
