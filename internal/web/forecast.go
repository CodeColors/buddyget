package web

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/codecolors/buddyget/internal/forecast"
	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/platform/money"
)

// forecastView est la donnée de l'écran du prévisionnel.
type forecastView struct {
	AccountID int64
	Target    string // date cible saisie
	Today     calendar.Date
	MaxTarget calendar.Date
	Accounts  []ledger.AccountBalance // comptes proposés
	Shortcuts []targetShortcut
	Error     string

	Report   *forecast.Report // nil si la saisie est invalide
	Chart    balanceChart
	Months   []forecastMonth
	Multiple bool   // plusieurs comptes : chaque ligne nomme le sien
	Self     string // adresse de la page, pour y revenir après une action

	Settings       forecast.Settings
	AverageChoices []int
}

// targetShortcut est une date cible proposée en un clic.
type targetShortcut struct {
	Label   string
	URL     string
	Current bool
}

// forecastMonth regroupe les lignes d'un mois avec le solde de fin de mois
// (ou de la date cible).
type forecastMonth struct {
	Label string
	Lines []forecast.Line
	End   int64
}

func (s *Server) forecastPage(w http.ResponseWriter, r *http.Request) {
	today := calendar.Today()
	v := forecastView{
		AccountID: formID(r, "compte"), Target: r.FormValue("date"), Today: today,
		MaxTarget: forecast.MaxTarget(today), AverageChoices: forecast.AverageChoices,
	}
	if v.Target == "" {
		v.Target = string(calendar.FromTime(today.Time().AddDate(0, 3, 0)))
	}
	accounts, err := s.ledger.Accounts(r.Context(), today)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	for _, a := range accounts {
		if !a.Archived || a.ID == v.AccountID {
			v.Accounts = append(v.Accounts, a)
		}
	}
	if v.Settings, err = s.forecast.Settings(r.Context()); err != nil {
		s.serverError(w, r, err)
		return
	}
	v.Shortcuts = shortcuts(today, v.AccountID, v.Target)
	v.Self = forecastURL(v.AccountID, v.Target)

	status := http.StatusOK
	if err := s.computeForecast(r, &v); err != nil {
		msg, ok := s.inputError(w, r, err)
		if !ok {
			return
		}
		v.Error, status = msg, http.StatusUnprocessableEntity
	}
	s.render(w, r, status, "forecast", "Prévisionnel", v)
}

func (s *Server) computeForecast(r *http.Request, v *forecastView) error {
	target, err := calendar.Parse(v.Target)
	if err != nil {
		return err
	}
	report, err := s.forecast.Forecast(r.Context(), forecast.Query{AccountID: v.AccountID, Target: target}, v.Today)
	if err != nil {
		return err
	}
	v.Report, v.Multiple = &report, len(report.Accounts) > 1
	v.Chart = newBalanceChart(report.Result)
	v.Months = groupByMonth(report.Result)
	return nil
}

// shortcuts propose la fin du mois (du suivant, le dernier jour) et des
// horizons de trois mois à deux ans.
func shortcuts(today calendar.Date, accountID int64, current string) []targetShortcut {
	t := today.Time()
	monthEnd := t.AddDate(0, 1, -t.Day())
	if calendar.FromTime(monthEnd) == today {
		monthEnd = t.AddDate(0, 2, -t.Day())
	}
	targets := []struct {
		label string
		date  calendar.Date
	}{
		{"Fin du mois", calendar.FromTime(monthEnd)},
		{"3 mois", calendar.FromTime(t.AddDate(0, 3, 0))},
		{"6 mois", calendar.FromTime(t.AddDate(0, 6, 0))},
		{"1 an", calendar.FromTime(t.AddDate(1, 0, 0))},
		{"2 ans", forecast.MaxTarget(today)},
	}
	out := make([]targetShortcut, len(targets))
	for i, tg := range targets {
		out[i] = targetShortcut{Label: tg.label, URL: forecastURL(accountID, string(tg.date)), Current: string(tg.date) == current}
	}
	return out
}

func groupByMonth(res forecast.Result) []forecastMonth {
	var out []forecastMonth
	for _, l := range res.Lines {
		label := l.Date.MonthLabel()
		if len(out) == 0 || out[len(out)-1].Label != label {
			out = append(out, forecastMonth{Label: label})
		}
		m := &out[len(out)-1]
		m.Lines = append(m.Lines, l)
	}
	// Solde du dernier jour du mois couvert par le prévisionnel.
	for i := range out {
		last := out[i].Lines[0].Date.Time()
		end := min(calendar.FromTime(last.AddDate(0, 1, -last.Day())), res.Target)
		out[i].End = res.Days[res.Today.DaysUntil(end)].Balance
	}
	return out
}

// forecastBack renvoie l'écran du prévisionnel d'où vient une action.
func forecastBack(r *http.Request) string {
	if back := r.PostFormValue("back"); strings.HasPrefix(back, "/previsionnel?") {
		return back
	}
	return "/previsionnel"
}

func forecastURL(accountID int64, target string) string {
	q := url.Values{"date": {target}}
	if accountID != 0 {
		q.Set("compte", strconv.FormatInt(accountID, 10))
	}
	return "/previsionnel?" + q.Encode()
}

// excludeOccurrence écarte une échéance du prévisionnel ou l'y réintègre.
func (s *Server) excludeOccurrence(w http.ResponseWriter, r *http.Request) {
	if parseForm(w, r) != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	back := forecastBack(r) + "#echeances"
	nominal, err := calendar.Parse(r.PostFormValue("date"))
	if err != nil {
		s.failed(w, r, err, back)
		return
	}
	excluded := r.PostFormValue("exclure") == "1"
	if err := s.forecast.SetExcluded(r.Context(), formID(r, "rule"), nominal, excluded, calendar.Today()); err != nil {
		s.failed(w, r, err, back)
		return
	}
	msg := "Échéance réintégrée au prévisionnel."
	if excluded {
		msg = "Échéance écartée du prévisionnel."
	}
	s.redirect(w, r, back, msg)
}

// saveForecastSettings enregistre le seuil, le mode de calcul du solde et la
// moyenne des dépenses variables.
func (s *Server) saveForecastSettings(w http.ResponseWriter, r *http.Request) {
	if parseForm(w, r) != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	back := forecastBack(r)
	threshold, err := money.Parse(r.PostFormValue("threshold"))
	if err != nil {
		s.failed(w, r, err, back)
		return
	}
	months, _ := strconv.Atoi(r.PostFormValue("average"))
	st := forecast.Settings{Threshold: threshold, ClearedOnly: r.PostFormValue("cleared") == "1", AverageMonths: months}
	if err := s.forecast.SaveSettings(r.Context(), st); err != nil {
		s.failed(w, r, err, back)
		return
	}
	s.redirect(w, r, back, "Réglages du prévisionnel enregistrés.")
}
