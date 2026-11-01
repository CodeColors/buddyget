package web

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/codecolors/buddyget/internal/forecast"
	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/loans"
	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/platform/money"
	"github.com/codecolors/buddyget/internal/platform/validation"
	"github.com/codecolors/buddyget/internal/recurring"
	"github.com/codecolors/buddyget/internal/scenarios"
)

func (s *Server) scenariosPage(w http.ResponseWriter, r *http.Request) {
	s.renderScenarios(w, r, http.StatusOK, "", "")
}

func (s *Server) renderScenarios(w http.ResponseWriter, r *http.Request, status int, name, msg string) {
	list, err := s.scenarios.Scenarios(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, status, "scenarios", "Scénarios", struct {
		Scenarios   []scenarios.Scenario
		Name, Error string
	}{list, name, msg})
}

func (s *Server) createScenario(w http.ResponseWriter, r *http.Request) {
	if parseForm(w, r) != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	name := r.PostFormValue("name")
	id, err := s.scenarios.Create(r.Context(), name)
	if err != nil {
		if msg, ok := s.inputError(w, r, err); ok {
			s.renderScenarios(w, r, http.StatusUnprocessableEntity, name, msg)
		}
		return
	}
	s.redirect(w, r, scenarioURL(id), "Scénario créé : ajoutez-lui des modifications.")
}

// scenarioHorizon est l'horizon par défaut de la comparaison.
const scenarioHorizon = 12

// scenarioView est la donnée de la fiche d'un scénario.
type scenarioView struct {
	scenarios.Scenario
	Items      []itemView
	Target     string
	Today      calendar.Date
	MaxTarget  calendar.Date
	Comparison *forecast.Comparison // nil si la date est invalide
	CompareErr string
	Forecast   string // adresse du prévisionnel avec le scénario
	Form       itemForm
	Kinds      []scenarios.Kind
	Accounts   []ledger.AccountBalance
	Rules      []recurring.Rule
	Loans      []loans.Loan
}

// itemView est une modification décrite en clair.
type itemView struct {
	scenarios.Item
	Detail string
}

func describe(it scenarios.Item) string {
	switch it.Kind {
	case scenarios.OneOff:
		return fmt.Sprintf("%s le %s · %s", money.FormatSigned(it.Amount), it.Date.Format(), it.AccountName)
	case scenarios.Repeating:
		every := "chaque mois"
		if it.Months > 1 {
			every = fmt.Sprintf("tous les %d mois", it.Months)
		}
		end := ""
		if it.End != "" {
			end = " au " + it.End.Format()
		}
		return fmt.Sprintf("%s %s, du %s%s · %s", money.FormatSigned(it.Amount), every, it.Date.Format(), end, it.AccountName)
	case scenarios.Suspend:
		return "Échéances supprimées à partir du " + it.Date.Format()
	case scenarios.Vary:
		change := money.FormatSigned(it.Amount)
		if it.Percent != 0 {
			change = strings.Replace(money.FormatSigned(int64(it.Percent)), " €", "", 1) + " %"
		}
		return fmt.Sprintf("%s à partir du %s", change, it.Date.Format())
	case scenarios.NewLoan:
		in := it.Loan()
		return fmt.Sprintf("%s à %s %% sur %d mois, %s par mois dès le %s · %s", money.Format(in.Principal), formatRate(in.Rate), in.Months,
			money.Format(loans.Payment(in.Params)+in.Insurance), it.Date.Format(), it.AccountName)
	case scenarios.Prepay:
		return fmt.Sprintf("%s le %s", money.Format(it.Amount), it.Date.Format())
	}
	return ""
}

// itemForm est la saisie d'une modification, en texte saisi.
type itemForm struct {
	Kind      scenarios.Kind
	Sens      string // depense ou revenu
	Amount    string
	Label     string
	AccountID int64
	Date      string
	End       string
	Every     string // mois entre deux échéances
	RuleID    int64
	Change    string // hausse ou baisse
	Mode      string // euros ou pourcent
	Value     string
	Principal string
	Rate      string
	Duration  string
	Insurance string
	LoanID    int64
	Repaid    string
	Error     string
}

func scenarioURL(id int64) string { return fmt.Sprintf("/scenarios/%d", id) }

func (s *Server) scenarioPage(w http.ResponseWriter, r *http.Request) {
	s.renderScenario(w, r, http.StatusOK, itemForm{Kind: scenarios.OneOff, Sens: typeExpense, Every: "1", Change: "hausse", Mode: "euros",
		Date: string(calendar.Today().AddDays(1))})
}

func (s *Server) renderScenario(w http.ResponseWriter, r *http.Request, status int, f itemForm) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	ctx, today := r.Context(), calendar.Today()
	sc, err := s.scenarios.Scenario(ctx, id)
	if err != nil {
		s.inputError(w, r, err)
		return
	}
	v := scenarioView{Scenario: sc, Today: today, MaxTarget: forecast.MaxTarget(today), Form: f, Kinds: scenarios.Kinds,
		Target: r.FormValue("date")}
	if v.Target == "" {
		v.Target = string(calendar.FromTime(today.Time().AddDate(0, scenarioHorizon, 0)))
	}
	for _, it := range sc.Items {
		v.Items = append(v.Items, itemView{Item: it, Detail: describe(it)})
	}
	if err := s.scenarioChoices(r, &v); err != nil {
		s.serverError(w, r, err)
		return
	}
	v.Forecast = forecastURL("", id, v.Target)
	if target, err := calendar.Parse(v.Target); err != nil {
		v.CompareErr, _ = validation.Message(err)
	} else if c, err := s.forecast.Compare(ctx, forecast.Query{Target: target, ScenarioID: id}, today); err == nil {
		v.Comparison = &c
	} else if msg, ok := validation.Message(err); ok {
		v.CompareErr = msg
	} else {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, status, "scenario", "Scénario "+sc.Name, v)
}

// scenarioChoices charge les comptes, récurrences et crédits proposés.
func (s *Server) scenarioChoices(r *http.Request, v *scenarioView) error {
	accounts, err := s.ledger.Accounts(r.Context(), v.Today)
	if err != nil {
		return err
	}
	for _, a := range accounts {
		if !a.Archived {
			v.Accounts = append(v.Accounts, a)
		}
	}
	rules, err := s.recurring.Rules(r.Context())
	if err != nil {
		return err
	}
	for _, rule := range rules {
		if rule.Active {
			v.Rules = append(v.Rules, rule)
		}
	}
	v.Loans, err = s.loans.Loans(r.Context())
	return err
}

func (s *Server) renameScenario(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	if parseForm(w, r) != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	if err := s.scenarios.Rename(r.Context(), id, r.PostFormValue("name")); err != nil {
		s.failed(w, r, err, scenarioURL(id))
		return
	}
	s.redirect(w, r, scenarioURL(id), "Scénario renommé.")
}

func (s *Server) addScenarioItem(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	if parseForm(w, r) != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	v := r.PostFormValue
	f := itemForm{
		Kind: scenarios.Kind(v("kind")), Sens: v("sens"), Amount: v("amount"), Label: v("label"), AccountID: formID(r, "account"),
		Date: v("date"), End: v("end"), Every: v("every"), RuleID: formID(r, "rule"), Change: v("change"), Mode: v("mode"),
		Value: v("value"), Principal: v("principal"), Rate: v("rate"), Duration: v("duration"), Insurance: v("insurance"),
		LoanID: formID(r, "loan"), Repaid: v("repaid"),
	}
	it, err := f.item()
	if err == nil {
		_, err = s.scenarios.AddItem(r.Context(), id, it)
	}
	if err != nil {
		if msg, ok := s.inputError(w, r, err); ok {
			f.Error = msg
			s.renderScenario(w, r, http.StatusUnprocessableEntity, f)
		}
		return
	}
	s.redirect(w, r, scenarioURL(id)+"#modifications", "Modification ajoutée au scénario.")
}

// item lit la saisie selon la nature de la modification ; le service
// valide le reste.
func (f itemForm) item() (scenarios.Item, error) {
	it := scenarios.Item{Kind: f.Kind, Label: f.Label, AccountID: f.AccountID, RuleID: f.RuleID, LoanID: f.LoanID}
	var err error
	if it.Date, err = calendar.Parse(f.Date); err != nil {
		return it, err
	}
	switch f.Kind {
	case scenarios.OneOff, scenarios.Repeating:
		if it.Amount, err = money.Parse(f.Amount); err != nil {
			return it, err
		}
		it.Amount = max(it.Amount, -it.Amount)
		if f.Sens != typeIncome {
			it.Amount = -it.Amount
		}
		if f.Kind == scenarios.Repeating {
			it.Months, _ = strconv.Atoi(strings.TrimSpace(f.Every))
			if strings.TrimSpace(f.End) != "" {
				it.End, err = calendar.Parse(f.End)
			}
		}
	case scenarios.Vary:
		value, err := money.Parse(f.Value)
		if err != nil {
			return it, err
		}
		value = max(value, -value)
		if f.Change == "baisse" {
			value = -value
		}
		if f.Mode == "pourcent" {
			it.Percent = int(value)
		} else {
			it.Amount = value
		}
	case scenarios.NewLoan:
		if it.Amount, err = money.Parse(f.Principal); err != nil {
			return it, err
		}
		if it.Rate, err = parseRate(f.Rate); err != nil {
			return it, err
		}
		it.Months, _ = strconv.Atoi(strings.TrimSpace(f.Duration))
		if strings.TrimSpace(f.Insurance) != "" {
			it.Insurance, err = money.Parse(f.Insurance)
		}
	case scenarios.Prepay:
		it.Amount, err = money.Parse(f.Repaid)
	}
	return it, err
}

func (s *Server) deleteScenarioItem(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	item, err := strconv.ParseInt(r.PathValue("item"), 10, 64)
	if !ok || err != nil {
		s.notFound(w, r)
		return
	}
	if err := s.scenarios.DeleteItem(r.Context(), id, item); err != nil {
		s.failed(w, r, err, scenarioURL(id))
		return
	}
	s.redirect(w, r, scenarioURL(id)+"#modifications", "Modification retirée du scénario.")
}

func (s *Server) convertScenario(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	if err := s.scenarios.Convert(r.Context(), id); err != nil {
		s.failed(w, r, err, scenarioURL(id))
		return
	}
	s.redirect(w, r, "/previsionnel", "Scénario converti en données réelles : opérations, récurrences et crédits sont enregistrés.")
}

func (s *Server) deleteScenario(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	if err := s.scenarios.Delete(r.Context(), id); err != nil {
		s.failed(w, r, err, "/scenarios")
		return
	}
	s.redirect(w, r, "/scenarios", "Scénario supprimé ; vos données réelles n'ont pas changé.")
}
