package forecast

import (
	"context"
	"errors"
	"time"

	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/platform/validation"
)

// Outlook est le reste à vivre du mois en cours sur les comptes courants :
// entrées reçues et attendues, moins les charges et dépenses prévues d'ici
// la fin du mois, les versements vers les autres comptes du foyer
// (épargne…) et les dépenses déjà faites. Il reprend les échéances et les
// opérations planifiées du prévisionnel, sans estimation des dépenses
// variables : c'est ce qui reste pour elles.
type Outlook struct {
	Month    calendar.Date // premier jour du mois
	Received int64         // entrées reçues
	Expected int64         // entrées attendues d'ici la fin du mois
	Upcoming int64         // charges fixes et dépenses prévues à venir (négatif)
	Savings  int64         // versements vers les autres comptes, faits et à venir (négatif)
	Spent    int64         // dépenses déjà faites (négatif)
}

// Left renvoie le reste à vivre.
func (o Outlook) Left() int64 { return o.Received + o.Expected + o.Upcoming + o.Savings + o.Spent }

// Outlook calcule le reste à vivre du mois de today ; faux sans compte
// courant actif.
func (s *Service) Outlook(ctx context.Context, today calendar.Date) (Outlook, bool, error) {
	var r Report
	if err := s.selectAccounts(ctx, &r, Query{}, today); err != nil {
		if _, ok := validation.Message(err); ok {
			return Outlook{}, false, nil
		}
		return Outlook{}, false, err
	}
	names := map[int64]string{}
	for _, a := range r.Accounts {
		names[a.ID] = a.Name
	}
	t := today.Time()
	o := Outlook{Month: calendar.FromTime(time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC))}
	if err := s.monthToDate(ctx, &o, names, today); err != nil {
		return o, false, err
	}

	in := Input{Today: today, Target: calendar.FromTime(o.Month.Time().AddDate(0, 1, -1))}
	rules, err := s.recurring.Rules(ctx)
	if err != nil {
		return o, false, err
	}
	err = errors.Join(s.occurrences(ctx, &in, rules, names), s.planned(ctx, &in, names))
	if err != nil {
		return o, false, err
	}
	for _, e := range in.Events {
		switch {
		case e.Excluded || e.Date <= today || e.Date > in.Target:
		case e.Amount > 0:
			o.Expected += e.Amount
		case e.Transfer:
			o.Savings += e.Amount
		default:
			o.Upcoming += e.Amount
		}
	}
	return o, true, nil
}

// monthToDate totalise les opérations du mois jusqu'à today, hors virements
// entre comptes courants (sans effet sur leur total).
func (s *Service) monthToDate(ctx context.Context, o *Outlook, names map[int64]string, today calendar.Date) error {
	ids, err := accountList(names)
	if err != nil {
		return err
	}
	return s.db.R.QueryRowContext(ctx, `
		WITH covered (id) AS (SELECT value FROM json_each(?)),
		moves AS (
			SELECT t.amount, o.account_id IS NOT NULL AS transfer FROM transactions t
			LEFT JOIN transactions o ON o.transfer_id = t.transfer_id AND o.id <> t.id
			WHERE t.account_id IN covered AND t.date >= ? AND t.date <= ?
				AND (o.account_id IS NULL OR o.account_id NOT IN covered))
		SELECT coalesce(sum(amount) FILTER (WHERE amount > 0), 0),
			coalesce(sum(amount) FILTER (WHERE amount < 0 AND transfer), 0),
			coalesce(sum(amount) FILTER (WHERE amount < 0 AND NOT transfer), 0)
		FROM moves`, ids, o.Month, today).Scan(&o.Received, &o.Savings, &o.Spent)
}
