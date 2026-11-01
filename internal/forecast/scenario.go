package forecast

import (
	"context"
	"slices"

	"github.com/codecolors/buddyget/internal/loans"
	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/scenarios"
)

// applyScenario applique les modifications d'un scénario, par date, aux
// récurrences projetées et aux mouvements attendus : opérations et
// récurrences ajoutées, échéances supprimées ou modifiées à partir d'une
// date, nouveau crédit (ses mensualités ; le capital finance directement
// l'achat) et remboursement anticipé d'un crédit suivi (le versement, puis
// les mensualités devenues inutiles supprimées).
func (s *Service) applyScenario(ctx context.Context, sc scenarios.Scenario, plans []plan, in *Input, names map[int64]string) ([]plan, error) {
	byRule := make(map[int64]int, len(plans))
	for i, p := range plans {
		byRule[p.ID] = i
	}
	extra := map[int64][]loans.Prepayment{} // remboursements anticipés simulés, par crédit
	for _, it := range sc.Items {
		switch it.Kind {
		case scenarios.OneOff:
			if name, ok := names[it.AccountID]; ok {
				in.Events = append(in.Events, Event{Date: it.Date, Label: it.Label, Amount: it.Amount, Kind: Planned,
					AccountID: it.AccountID, AccountName: name, Scenario: true})
			}
		case scenarios.Repeating:
			plans = append(plans, plan{Rule: it.Rule().Draft(), simulated: true})
		case scenarios.Suspend:
			if i, ok := byRule[it.RuleID]; ok {
				plans[i].set(it.Date, 0)
			}
		case scenarios.Vary:
			if i, ok := byRule[it.RuleID]; ok {
				p := &plans[i]
				from := p.ResumeOn(it.Date)
				p.set(from, it.Apply(p.amountAt(from)))
			}
		case scenarios.NewLoan:
			l := it.Loan()
			plans = append(plans, plan{Rule: loans.Loan{Name: l.Name, AccountID: l.AccountID, Params: l.Params}.Rule().Draft(), simulated: true})
		case scenarios.Prepay:
			l, err := s.loans.Loan(ctx, it.LoanID)
			if err != nil {
				return nil, err
			}
			before := loans.Amortize(l.Params, slices.Concat(l.Prepayments, extra[l.ID]))
			extra[l.ID] = append(extra[l.ID], loans.Prepayment{Date: it.Date, Amount: it.Amount})
			after := loans.Amortize(l.Params, slices.Concat(l.Prepayments, extra[l.ID]))
			if name, ok := names[l.AccountID]; ok && after.TotalPrepaid > before.TotalPrepaid {
				in.Events = append(in.Events, Event{Date: it.Date, Label: "Remboursement anticipé : " + l.Name,
					Amount: before.TotalPrepaid - after.TotalPrepaid, Kind: Planned, AccountID: l.AccountID, AccountName: name, Scenario: true})
			}
			if i, ok := byRule[l.RuleID]; ok && after.End != "" {
				plans[i].set(after.End.AddDays(1), 0)
			}
		}
	}
	return plans, nil
}

// Comparison met un scénario en regard du prévisionnel de référence.
type Comparison struct {
	Reference, Scenario Report
}

// Gap renvoie l'écart cumulé du scénario à la date cible.
func (c Comparison) Gap() int64 { return c.Scenario.Closing - c.Reference.Closing }

// LowGap renvoie l'écart entre les points bas.
func (c Comparison) LowGap() int64 { return c.Scenario.Low.Balance - c.Reference.Low.Balance }

// Compare calcule le prévisionnel avec le scénario q.ScenarioID et sans.
func (s *Service) Compare(ctx context.Context, q Query, today calendar.Date) (Comparison, error) {
	var c Comparison
	var err error
	if c.Scenario, err = s.Forecast(ctx, q, today); err != nil {
		return c, err
	}
	q.ScenarioID = 0
	c.Reference, err = s.Forecast(ctx, q, today)
	return c, err
}
