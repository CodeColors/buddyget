package web

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/platform/money"
	"github.com/codecolors/buddyget/internal/recurring"
)

// ruleWithNext associe une récurrence à sa prochaine échéance.
type ruleWithNext struct {
	recurring.Rule
	Next     calendar.Date
	Finished bool
}

// recurrencesView est la donnée de la liste des récurrences.
type recurrencesView struct {
	Active   []ruleWithNext
	Inactive []ruleWithNext
	Detected int
}

func (s *Server) recurrencesPage(w http.ResponseWriter, r *http.Request) {
	rules, err := s.recurring.Rules(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	today := calendar.Today()
	var v recurrencesView
	for _, rule := range rules {
		next, ok := rule.Next(today)
		item := ruleWithNext{Rule: rule, Next: next, Finished: !ok}
		if rule.Active {
			v.Active = append(v.Active, item)
		} else {
			v.Inactive = append(v.Inactive, item)
		}
	}
	detected, err := s.recurring.Detected(r.Context(), today)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	v.Detected = len(detected)
	s.render(w, r, http.StatusOK, "recurrences", "Récurrences", v)
}

// detectedView est une série détectée, avec les noms à afficher.
type detectedView struct {
	recurring.Candidate
	AccountName  string
	CategoryName string
}

func (s *Server) detectedPage(w http.ResponseWriter, r *http.Request) {
	candidates, err := s.recurring.Detected(r.Context(), calendar.Today())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	names, err := s.names(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	out := make([]detectedView, len(candidates))
	for i, c := range candidates {
		out[i] = detectedView{Candidate: c, AccountName: names.accounts[c.AccountID], CategoryName: names.categories[c.CategoryID]}
	}
	s.render(w, r, http.StatusOK, "recurrences_detected", "Récurrences détectées", out)
}

// nameIndex donne le nom des comptes et des catégories par identifiant.
type nameIndex struct {
	accounts   map[int64]string
	categories map[int64]string
}

func (s *Server) names(r *http.Request) (nameIndex, error) {
	idx := nameIndex{accounts: map[int64]string{}, categories: map[int64]string{}}
	accounts, err := s.ledger.Accounts(r.Context(), calendar.Today())
	if err != nil {
		return idx, err
	}
	for _, a := range accounts {
		idx.accounts[a.ID] = a.Name
	}
	cats, err := s.ledger.Categories(r.Context())
	for _, c := range cats {
		idx.categories[c.ID] = c.FullName()
	}
	return idx, err
}

func (s *Server) acceptDetected(w http.ResponseWriter, r *http.Request) {
	if parseForm(w, r) != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	if _, err := s.recurring.Accept(r.Context(), r.PostFormValue("signature"), calendar.Today()); err != nil {
		s.failed(w, r, err, "/recurrences/detectees")
		return
	}
	s.redirect(w, r, "/recurrences/detectees", "Récurrence validée.")
}

func (s *Server) dismissDetected(w http.ResponseWriter, r *http.Request) {
	if parseForm(w, r) != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	if err := s.recurring.Dismiss(r.Context(), r.PostFormValue("signature")); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.redirect(w, r, "/recurrences/detectees", "Série rejetée : elle ne sera plus proposée.")
}

// Périodicités proposées dans le formulaire ; « autre » ouvre la saisie
// d'un nombre et d'une unité.
var periodChoices = []calendar.Period{
	calendar.Weekly, calendar.Monthly, calendar.Bimonthly, calendar.Quarterly, calendar.Semiannual, calendar.Yearly,
}

const customPeriod = "autre"

func periodValue(p calendar.Period) string {
	return strconv.Itoa(p.Count) + "-" + string(p.Unit)
}

// recurrenceForm est la donnée du formulaire de récurrence, en texte saisi.
type recurrenceForm struct {
	ID          int64
	Active      bool
	Origin      recurring.Origin
	Series      string // signature de la série détectée d'origine
	Type        string // depense ou revenu
	Amount      string
	Label       string
	AccountID   int64
	CategoryID  int64
	Period      string // valeur d'un choix, ou customPeriod
	CustomCount int
	CustomUnit  calendar.Unit
	LastDay     bool
	Estimated   bool
	Start       string
	End         string
	Error       string

	PeriodChoices []calendar.Period
	Units         []calendar.Unit
	Accounts      []ledger.AccountBalance
	Categories    []ledger.KindGroup
}

func (f recurrenceForm) title() string {
	if f.ID == 0 {
		return "Nouvelle récurrence"
	}
	return "Modifier la récurrence"
}

// PeriodValue renvoie la valeur d'option d'une périodicité (gabarit).
func (f recurrenceForm) PeriodValue(p calendar.Period) string { return periodValue(p) }

// formFromRule prépare le formulaire à partir d'une saisie existante.
func formFromRule(id int64, in recurring.RuleInput) recurrenceForm {
	f := recurrenceForm{
		ID: id, Origin: in.Origin, Series: in.Signature, Type: typeExpense, Amount: money.FormatInput(in.Amount), Label: in.Label,
		AccountID: in.AccountID, CategoryID: in.CategoryID, LastDay: in.LastDay, Estimated: in.Estimated,
		Start: string(in.Start), End: string(in.End), CustomCount: in.Period.Count, CustomUnit: in.Period.Unit,
		Period: customPeriod,
	}
	if in.Amount > 0 {
		f.Type = typeIncome
	}
	for _, p := range periodChoices {
		if p == in.Period {
			f.Period = periodValue(p)
		}
	}
	return f
}

func (s *Server) renderRecurrenceForm(w http.ResponseWriter, r *http.Request, status int, f recurrenceForm) {
	accounts, err := s.ledger.Accounts(r.Context(), calendar.Today())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	for _, a := range accounts {
		if !a.Archived || a.ID == f.AccountID {
			f.Accounts = append(f.Accounts, a)
		}
	}
	cats, err := s.ledger.Categories(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	f.Categories = ledger.GroupByKind(cats)
	f.PeriodChoices, f.Units = periodChoices, []calendar.Unit{calendar.Day, calendar.Week, calendar.Month}
	s.render(w, r, status, "recurrence_form", f.title(), f)
}

// newRecurrencePage ouvre un formulaire vierge, ou prérempli d'après une
// série détectée (« Modifier » avant de valider).
func (s *Server) newRecurrencePage(w http.ResponseWriter, r *http.Request) {
	in := recurring.RuleInput{Period: calendar.Monthly, Start: calendar.Today(), Origin: recurring.Manual}
	if sig := r.URL.Query().Get("serie"); sig != "" {
		c, err := s.recurring.Candidate(r.Context(), sig, calendar.Today())
		if err != nil {
			s.failed(w, r, err, "/recurrences/detectees")
			return
		}
		in = c.Input()
	}
	f := formFromRule(0, in)
	if in.Amount == 0 {
		f.Amount = "" // saisie vierge
	}
	s.renderRecurrenceForm(w, r, http.StatusOK, f)
}

func (s *Server) editRecurrencePage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	rule, err := s.recurring.Rule(r.Context(), id)
	if err != nil {
		s.inputError(w, r, err)
		return
	}
	f := formFromRule(id, recurring.RuleInput{
		Label: rule.Label, AccountID: rule.AccountID, Amount: rule.Amount, Estimated: rule.Estimated, Period: rule.Period,
		LastDay: rule.LastDay, Start: rule.Start, End: rule.End, CategoryID: rule.CategoryID, Origin: rule.Origin,
	})
	f.Active = rule.Active
	s.renderRecurrenceForm(w, r, http.StatusOK, f)
}

func (s *Server) createRecurrence(w http.ResponseWriter, r *http.Request) {
	s.saveRecurrence(w, r, 0)
}

func (s *Server) updateRecurrence(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	s.saveRecurrence(w, r, id)
}

func (s *Server) saveRecurrence(w http.ResponseWriter, r *http.Request, id int64) {
	if err := parseForm(w, r); err != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	count, _ := strconv.Atoi(r.PostFormValue("custom_count"))
	f := recurrenceForm{
		ID: id, Type: r.PostFormValue("type"), Amount: r.PostFormValue("amount"), Label: r.PostFormValue("label"),
		AccountID: formID(r, "account"), CategoryID: formID(r, "category"), Period: r.PostFormValue("period"),
		CustomCount: count, CustomUnit: calendar.Unit(r.PostFormValue("custom_unit")),
		LastDay: r.PostFormValue("last_day") != "", Estimated: r.PostFormValue("estimated") != "",
		Start: r.PostFormValue("start"), End: r.PostFormValue("end"), Origin: recurring.Manual,
	}
	if r.PostFormValue("origin") == string(recurring.Detected) {
		f.Origin, f.Series = recurring.Detected, r.PostFormValue("serie")
	}
	if err := s.storeRecurrence(r, f); err != nil {
		if msg, ok := s.inputError(w, r, err); ok {
			f.Error = msg
			s.renderRecurrenceForm(w, r, http.StatusUnprocessableEntity, f)
		}
		return
	}
	s.redirect(w, r, "/recurrences", "Récurrence enregistrée.")
}

func (s *Server) storeRecurrence(r *http.Request, f recurrenceForm) error {
	amount, err := money.Parse(f.Amount)
	if err != nil {
		return err
	}
	amount = max(amount, -amount)
	if f.Type != typeIncome {
		amount = -amount
	}
	start, err := calendar.Parse(f.Start)
	if err != nil {
		return err
	}
	var end calendar.Date
	if strings.TrimSpace(f.End) != "" {
		if end, err = calendar.Parse(f.End); err != nil {
			return err
		}
	}
	period := calendar.Period{Count: f.CustomCount, Unit: f.CustomUnit}
	for _, p := range periodChoices {
		if periodValue(p) == f.Period {
			period = p
		}
	}
	in := recurring.RuleInput{
		Label: f.Label, AccountID: f.AccountID, Amount: amount, Estimated: f.Estimated, Period: period,
		LastDay: f.LastDay, Start: start, End: end, CategoryID: f.CategoryID, Origin: f.Origin, Signature: f.Series,
	}
	if f.ID == 0 {
		_, err = s.recurring.CreateRule(r.Context(), in)
		return err
	}
	return s.recurring.UpdateRule(r.Context(), f.ID, in)
}

func (s *Server) toggleRecurrence(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok || parseForm(w, r) != nil {
		s.notFound(w, r)
		return
	}
	active, _ := strconv.ParseBool(r.PostFormValue("active"))
	if err := s.recurring.SetActive(r.Context(), id, active); err != nil {
		s.failed(w, r, err, "/recurrences")
		return
	}
	msg := "Récurrence suspendue."
	if active {
		msg = "Récurrence réactivée."
	}
	s.redirect(w, r, "/recurrences", msg)
}

func (s *Server) deleteRecurrence(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	if err := s.recurring.DeleteRule(r.Context(), id); err != nil {
		s.failed(w, r, err, "/recurrences")
		return
	}
	s.redirect(w, r, "/recurrences", "Récurrence supprimée.")
}

func (s *Server) morePage(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "plus", "Plus", nil)
}
