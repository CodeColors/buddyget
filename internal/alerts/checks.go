package alerts

import (
	"context"
	"fmt"
	"slices"
	"strconv"

	"github.com/codecolors/buddyget/internal/budget"
	"github.com/codecolors/buddyget/internal/forecast"
	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/platform/money"
	"github.com/codecolors/buddyget/internal/platform/validation"
	"github.com/codecolors/buddyget/internal/recurring"
)

// Paramètres fixes des examens.
const (
	unusualWindow     = 7     // jours : dépenses récentes examinées
	unusualHistory    = 365   // jours : historique servant de référence
	unusualMinAmount  = 20_00 // en deçà, une dépense n'est jamais inhabituelle
	unusualMinSeries  = 3     // dépenses du même libellé pour établir l'habitude
	unusualMinInCat   = 5     // à défaut, dépenses de la même catégorie
	subscriptionDays  = 45    // jours : prélèvements examinés
	subscriptionDrift = 3     // jours d'écart tolérés sur la date
)

// Evaluate examine la situation au jour today et enregistre les alertes
// nouvelles des types activés ; renvoie leur nombre.
func (s *Service) Evaluate(ctx context.Context, today calendar.Date) (int, error) {
	settings, err := s.Settings(ctx)
	if err != nil {
		return 0, err
	}
	checks := map[Kind]func(context.Context, calendar.Date, int) ([]Alert, error){
		BudgetKind: s.checkBudgets, LowBalance: s.checkLowBalance, Unusual: s.checkUnusual,
		Subscription: s.checkSubscriptions, DueSoon: s.checkDueSoon, ImportStale: s.checkImports, Unlock: s.checkUnlocks,
	}
	var found []Alert
	for _, st := range settings {
		if !st.Enabled {
			continue
		}
		alerts, err := checks[st.Kind](ctx, today, st.Param)
		if err != nil {
			return 0, fmt.Errorf("alertes %s : %w", st.Kind, err)
		}
		for i := range alerts {
			alerts[i].Kind = st.Kind
		}
		found = append(found, alerts...)
	}
	return s.record(ctx, found, today)
}

// checkBudgets signale les enveloppes du mois consommées à risk % ou plus,
// puis dépassées.
func (s *Service) checkBudgets(ctx context.Context, today calendar.Date, risk int) ([]Alert, error) {
	statuses, err := s.budgets.Month(ctx, today)
	if err != nil {
		return nil, err
	}
	month := string(today)[:7]
	var out []Alert
	for _, st := range statuses {
		a := Alert{Link: "/budgets?mois=" + month}
		switch st.Level(risk) {
		case budget.Over:
			a.Key = fmt.Sprintf("budget:%d:%s:depasse", st.CategoryID, month)
			a.Title = "Enveloppe « " + st.CategoryName + " » dépassée"
			a.Detail = money.Format(st.Spent) + " dépensés pour " + money.Format(st.Available) + " prévus."
		case budget.Risk:
			a.Key = fmt.Sprintf("budget:%d:%s:risque", st.CategoryID, month)
			a.Title = "Enveloppe « " + st.CategoryName + " » presque épuisée"
			a.Detail = strconv.Itoa(st.Percent()) + " % consommés : il reste " + money.Format(st.Remaining()) + "."
		default:
			continue
		}
		out = append(out, a)
	}
	return out, nil
}

// checkLowBalance signale la première période où le solde prévu des comptes
// courants passe sous le seuil dans les days prochains jours.
func (s *Service) checkLowBalance(ctx context.Context, today calendar.Date, days int) ([]Alert, error) {
	target := today.AddDays(days)
	r, err := s.forecast.Forecast(ctx, forecast.Query{Target: target}, today)
	if _, input := validation.Message(err); input {
		return nil, nil // aucun compte courant
	}
	if err != nil || len(r.Below) == 0 {
		return nil, err
	}
	below := r.Below[0]
	return []Alert{{
		Key:    "prevision:" + string(below.From),
		Title:  "Solde prévu sous le seuil à partir du " + below.From.Format(),
		Detail: "Au plus bas " + money.Format(below.Low) + " (seuil : " + money.Format(r.Threshold) + ").",
		Link:   "/previsionnel?date=" + string(target),
	}}, nil
}

// op est une dépense examinée par les alertes.
type op struct {
	id, account, category int64
	date                  calendar.Date
	label                 string
	amount                int64
}

// expenses lit les dépenses (hors virements internes) depuis from.
func (s *Service) expenses(ctx context.Context, from, to calendar.Date) ([]op, error) {
	rows, err := s.db.R.QueryContext(ctx, `
		SELECT id, account_id, coalesce(category_id, 0), date, label, amount FROM transactions
		WHERE transfer_id IS NULL AND amount < 0 AND date >= ? AND date <= ? ORDER BY date, id`, from, to)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []op
	for rows.Next() {
		var o op
		if err := rows.Scan(&o.id, &o.account, &o.category, &o.date, &o.label, &o.amount); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// checkUnusual signale les dépenses des derniers jours supérieures à factor
// fois l'habitude : la médiane des dépenses du même libellé, à défaut de la
// même catégorie, sur un an.
func (s *Service) checkUnusual(ctx context.Context, today calendar.Date, factor int) ([]Alert, error) {
	ops, err := s.expenses(ctx, today.AddDays(-unusualHistory), today)
	if err != nil {
		return nil, err
	}
	bySeries, byCategory := map[string][]int64{}, map[int64][]int64{}
	for _, o := range ops {
		bySeries[recurring.Signature(o.account, o.amount, o.label)] = append(bySeries[recurring.Signature(o.account, o.amount, o.label)], -o.amount)
		if o.category != 0 {
			byCategory[o.category] = append(byCategory[o.category], -o.amount)
		}
	}
	since := today.AddDays(-unusualWindow)
	var out []Alert
	for _, o := range ops {
		if o.date < since || -o.amount < unusualMinAmount {
			continue
		}
		norm, ok := medianWithout(bySeries[recurring.Signature(o.account, o.amount, o.label)], -o.amount, unusualMinSeries)
		if !ok {
			norm, ok = medianWithout(byCategory[o.category], -o.amount, unusualMinInCat)
		}
		if !ok || -o.amount <= int64(factor)*norm {
			continue
		}
		out = append(out, Alert{
			Key:    fmt.Sprintf("inhabituelle:%d", o.id),
			Title:  "Dépense inhabituelle : " + o.label,
			Detail: money.Format(o.amount) + " le " + o.date.Format() + ", habituellement " + money.Format(-norm) + ".",
			Link:   fmt.Sprintf("/operations/%d", o.id),
		})
	}
	return out, nil
}

// medianWithout renvoie la médiane des valeurs, l'une d'elles égale à
// exclude en moins (la dépense examinée), s'il en reste au moins minCount.
func medianWithout(values []int64, exclude int64, minCount int) (int64, bool) {
	rest := slices.Clone(values)
	if i := slices.Index(rest, exclude); i >= 0 {
		rest = slices.Delete(rest, i, i+1)
	}
	if len(rest) < minCount {
		return 0, false
	}
	slices.Sort(rest)
	return rest[len(rest)/2], true
}

// checkSubscriptions compare le dernier prélèvement de chaque récurrence
// active (même série d'opérations) à son montant et à ses échéances.
func (s *Service) checkSubscriptions(ctx context.Context, today calendar.Date, tolerance int) ([]Alert, error) {
	rules, err := s.recurring.Rules(ctx)
	if err != nil {
		return nil, err
	}
	ops, err := s.expenses(ctx, today.AddDays(-subscriptionDays), today)
	if err != nil {
		return nil, err
	}
	latest := map[string]op{}
	for _, o := range ops { // dans l'ordre chronologique : le dernier l'emporte
		latest[recurring.Signature(o.account, o.amount, o.label)] = o
	}
	var out []Alert
	for _, r := range rules {
		o, ok := latest[r.Signature]
		if !r.Active || r.IsTransfer() || !ok || o.date < r.Start {
			continue
		}
		link := fmt.Sprintf("/recurrences/%d", r.ID)
		if diff := abs(o.amount - r.Amount); !r.Estimated && diff*100 > int64(tolerance)*abs(r.Amount) {
			out = append(out, Alert{
				Key:    fmt.Sprintf("abonnement:%d:%d:montant", r.ID, o.id),
				Title:  r.Label + " : montant modifié",
				Detail: money.Format(o.amount) + " prélevés le " + o.date.Format() + " au lieu de " + money.Format(r.Amount) + ".",
				Link:   link,
			})
		}
		if due, ok := nearestDue(r, o.date); ok && abs(int64(due.DaysUntil(o.date))) > subscriptionDrift {
			out = append(out, Alert{
				Key:    fmt.Sprintf("abonnement:%d:%d:date", r.ID, o.id),
				Title:  r.Label + " : date modifiée",
				Detail: "Prélevé le " + o.date.Format() + ", attendu le " + due.Format() + ".",
				Link:   link,
			})
		}
	}
	return out, nil
}

// nearestDue renvoie l'échéance de la récurrence la plus proche d'une date.
func nearestDue(r recurring.Rule, d calendar.Date) (calendar.Date, bool) {
	var best calendar.Date
	found := false
	for o := range r.Occurrences(calendar.FromTime(d.Time().AddDate(0, 0, -int(r.Period.Days())-calendar.MaxShift))) {
		if !found || abs(int64(o.Due.DaysUntil(d))) < abs(int64(best.DaysUntil(d))) {
			best, found = o.Due, true
		}
		if o.Due > d {
			break
		}
	}
	return best, found
}

// checkDueSoon annonce les échéances des récurrences actives d'ici days
// jours (aujourd'hui compris).
func (s *Service) checkDueSoon(ctx context.Context, today calendar.Date, days int) ([]Alert, error) {
	rules, err := s.recurring.Rules(ctx)
	if err != nil {
		return nil, err
	}
	until := today.AddDays(days)
	var out []Alert
	for _, r := range rules {
		if !r.Active {
			continue
		}
		for o := range r.Occurrences(today) {
			if o.Due > until {
				break
			}
			account := r.AccountName
			if r.IsTransfer() {
				account += " → " + r.ToAccountName
			}
			out = append(out, Alert{
				Key:    fmt.Sprintf("echeance:%d:%s", r.ID, o.Nominal),
				Title:  r.Label + " le " + o.Due.Format(),
				Detail: money.FormatSigned(r.Amount) + " sur " + account + ".",
				Link:   fmt.Sprintf("/recurrences/%d", r.ID),
			})
		}
	}
	return out, nil
}

// checkImports signale l'absence d'import depuis days jours (une fois
// qu'un premier import a eu lieu).
func (s *Service) checkImports(ctx context.Context, today calendar.Date, days int) ([]Alert, error) {
	var last string
	err := s.db.R.QueryRowContext(ctx, `SELECT coalesce(max(substr(imported_at, 1, 10)), '') FROM imports WHERE undone_at IS NULL`).Scan(&last)
	if err != nil || last == "" {
		return nil, err
	}
	lastDay := calendar.Date(last)
	if n := lastDay.DaysUntil(today); n < days {
		return nil, nil
	}
	return []Alert{{
		Key:    "import:" + last,
		Title:  "Aucun relevé importé depuis le " + lastDay.Format(),
		Detail: "Importez vos derniers relevés pour garder soldes et prévisions à jour.",
		Link:   "/import",
	}}, nil
}

// checkUnlocks annonce les fonds d'épargne débloqués d'ici days jours.
func (s *Service) checkUnlocks(ctx context.Context, today calendar.Date, days int) ([]Alert, error) {
	summaries, err := s.savings.Overview(ctx, today)
	if err != nil {
		return nil, err
	}
	until := today.AddDays(days)
	var out []Alert
	for _, sum := range summaries {
		next := sum.Availability.NextUnlock
		if next == "" || next > until {
			continue
		}
		out = append(out, Alert{
			Key:    fmt.Sprintf("deblocage:%d:%s", sum.Account.ID, next),
			Title:  "Déblocage de fonds le " + next.Format() + " : " + sum.Account.Name,
			Detail: "Fonds actuellement bloqués : " + money.Format(sum.Availability.Blocked) + ".",
			Link:   "/epargne",
		})
	}
	return out, nil
}

func abs(v int64) int64 { return max(v, -v) }
