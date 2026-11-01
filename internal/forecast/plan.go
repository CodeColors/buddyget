package forecast

import (
	"slices"

	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/recurring"
)

// plan est une récurrence telle que le prévisionnel la projette : une
// règle enregistrée, éventuellement modifiée par un scénario à partir de
// certaines échéances, ou une règle ajoutée par un scénario (simulated).
type plan struct {
	recurring.Rule
	changes   []change // par date
	simulated bool
}

// change fixe le montant des échéances à partir d'une date théorique ; un
// montant nul arrête la récurrence.
type change struct {
	from   calendar.Date
	amount int64
}

func plansOf(rules []recurring.Rule) []plan {
	plans := make([]plan, len(rules))
	for i, r := range rules {
		plans[i].Rule = r
	}
	return plans
}

// set modifie le montant des échéances à partir de from ; à date égale, le
// dernier changement l'emporte.
func (p *plan) set(from calendar.Date, amount int64) {
	i := slices.IndexFunc(p.changes, func(c change) bool { return c.from > from })
	if i < 0 {
		i = len(p.changes)
	}
	p.changes = slices.Insert(p.changes, i, change{from, amount})
}

// amountAt renvoie le montant de l'échéance de date théorique nominal ; 0
// si un scénario l'a supprimée.
func (p plan) amountAt(nominal calendar.Date) int64 {
	a := p.Amount
	for _, c := range p.changes {
		if c.from > nominal {
			break
		}
		if c.amount == 0 {
			return 0
		}
		a = c.amount
	}
	return a
}
