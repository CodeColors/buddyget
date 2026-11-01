package web

import (
	"net/http"
	"strconv"

	"github.com/codecolors/buddyget/internal/forecast"
	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/recurring"
	"github.com/codecolors/buddyget/internal/savings"
)

// historyMonths est la profondeur de la courbe d'évolution de l'épargne.
const historyMonths = 24

// savingsView est la donnée de la vue Épargne.
type savingsView struct {
	Accounts []savingsAccount
	Totals   savingsTotals
	History  *balanceChart // nil sans historique suffisant
	// Projection de l'épargne à la date choisie, par le prévisionnel.
	Target     string
	Projection *forecast.Report
	Projected  savingsProjection
	Error      string
	Templates  []savings.Template
}

// savingsAccount est un compte d'épargne et ses versements programmés.
type savingsAccount struct {
	savings.Summary
	Deposits []recurring.Rule
}

type savingsTotals struct {
	Balance, Available, Blocked, InterestPaid, Accrued int64
}

// savingsProjection ventile ce qui s'ajoute d'ici la date de projection.
type savingsProjection struct {
	Interest int64 // intérêts estimés
	Moves    int64 // versements et retraits prévus
}

func (s *Server) savingsPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	today := calendar.Today()
	v := savingsView{Target: r.FormValue("date"), Templates: savings.Templates}
	if v.Target == "" {
		v.Target = string(calendar.FromTime(today.Time().AddDate(1, 0, 0)))
	}
	summaries, err := s.savings.Overview(ctx, today)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	rules, err := s.recurring.Rules(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	for _, sum := range summaries {
		a := savingsAccount{Summary: sum}
		for _, rule := range rules {
			if rule.Active && rule.ToAccountID == sum.Account.ID {
				a.Deposits = append(a.Deposits, rule)
			}
		}
		v.Accounts = append(v.Accounts, a)
		v.Totals.Balance += sum.Account.Balance
		v.Totals.Available += sum.Availability.Available
		v.Totals.Blocked += sum.Availability.Blocked
		v.Totals.InterestPaid += sum.InterestPaid
		v.Totals.Accrued += sum.Accrued
	}
	if len(v.Accounts) > 0 {
		if err := s.savingsHistory(r, &v, today); err != nil {
			s.serverError(w, r, err)
			return
		}
		if err := s.savingsProjection(r, &v, today); err != nil {
			msg, ok := s.inputError(w, r, err)
			if !ok {
				return
			}
			v.Error = msg
		}
	}
	s.render(w, r, http.StatusOK, "savings", "Épargne", v)
}

func (s *Server) savingsHistory(r *http.Request, v *savingsView, today calendar.Date) error {
	history, err := s.savings.History(r.Context(), today, historyMonths)
	if err != nil || len(history) < 2 {
		return err
	}
	points := make([]forecast.Point, len(history))
	for i, h := range history {
		points[i] = forecast.Point{Date: h.Date, Balance: h.Amount}
	}
	c, _ := lineChart("evolution", points, false)
	c.Desc = "Total épargné à la fin de chaque mois, de " + history[0].Date.MonthLabel() + " à aujourd'hui."
	v.History = &c
	return nil
}

func (s *Server) savingsProjection(r *http.Request, v *savingsView, today calendar.Date) error {
	target, err := calendar.Parse(v.Target)
	if err != nil {
		return err
	}
	report, err := s.forecast.Forecast(r.Context(), forecast.Query{Savings: true, Target: target}, today)
	if err != nil {
		return err
	}
	v.Projection = &report
	for _, l := range report.Lines {
		switch {
		case l.Excluded:
		case l.Kind == forecast.Interest:
			v.Projected.Interest += l.Amount
		default:
			v.Projected.Moves += l.Amount
		}
	}
	return nil
}

// recordInterest inscrit les intérêts d'une année close d'un compte.
func (s *Server) recordInterest(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok || parseForm(w, r) != nil {
		s.notFound(w, r)
		return
	}
	year, _ := strconv.Atoi(r.PostFormValue("annee"))
	if err := s.savings.RecordInterest(r.Context(), id, year, calendar.Today()); err != nil {
		s.failed(w, r, err, "/epargne")
		return
	}
	s.redirect(w, r, "/epargne", "Intérêts "+strconv.Itoa(year)+" inscrits : modifiez l'opération si la banque a versé un autre montant.")
}
