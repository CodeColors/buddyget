package scenarios

import (
	"context"

	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/loans"
	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/recurring"
)

// Convert transforme un scénario en données réelles, modification par
// modification dans l'ordre des dates, puis le supprime : opérations
// planifiées, récurrences créées, arrêtées ou scindées, crédits et
// remboursements anticipés enregistrés.
func (s *Service) Convert(ctx context.Context, id int64) error {
	sc, err := s.Scenario(ctx, id)
	if err != nil {
		return err
	}
	for _, it := range sc.Items {
		if err := s.convert(ctx, it); err != nil {
			return err
		}
	}
	return s.Delete(ctx, id)
}

func (s *Service) convert(ctx context.Context, it Item) error {
	var err error
	switch it.Kind {
	case OneOff:
		_, err = s.ledger.CreateTransaction(ctx, ledger.TransactionInput{AccountID: it.AccountID, Date: it.Date, Label: it.Label, Amount: it.Amount})
	case Repeating:
		_, err = s.recurring.CreateRule(ctx, it.Rule())
	case Suspend, Vary:
		err = s.change(ctx, it)
	case NewLoan:
		_, err = s.loans.Create(ctx, it.Loan())
	case Prepay:
		err = s.loans.Prepay(ctx, it.LoanID, loans.Prepayment{Date: it.Date, Amount: it.Amount})
	}
	return err
}

// Rule renvoie la récurrence d'une opération récurrente ajoutée.
func (it Item) Rule() recurring.RuleInput {
	return recurring.RuleInput{
		Label: it.Label, AccountID: it.AccountID, Amount: it.Amount, Period: it.Period(), Shift: calendar.NextBusinessDay,
		Start: it.Date, End: it.End, Origin: recurring.Manual,
	}
}

// change arrête une récurrence avant la première échéance concernée et, pour
// une variation, la poursuit au nouveau montant dans une nouvelle récurrence.
// Une récurrence concernée dès sa première échéance est suspendue, ou
// modifiée sur place.
func (s *Service) change(ctx context.Context, it Item) error {
	rule, err := s.recurring.Rule(ctx, it.RuleID)
	if err != nil {
		return err
	}
	from, amount := it.Date, int64(0)
	if it.Kind == Vary {
		from, amount = rule.ResumeOn(it.Date), it.Apply(rule.Amount)
	}
	switch {
	case rule.End != "" && from > rule.End:
		return nil // déjà terminée
	case from <= rule.Start && amount == 0:
		return s.recurring.SetActive(ctx, rule.ID, false)
	case from <= rule.Start:
		in := rule.Input()
		in.Amount = amount
		return s.recurring.UpdateRule(ctx, rule.ID, in)
	}
	if amount != 0 {
		next := rule.Input()
		next.Start, next.Amount = from, amount
		if _, err := s.recurring.CreateRule(ctx, next); err != nil {
			return err
		}
	}
	before := rule.Input()
	before.End = from.AddDays(-1)
	return s.recurring.UpdateRule(ctx, rule.ID, before)
}
