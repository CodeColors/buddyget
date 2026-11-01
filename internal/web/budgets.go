package web

import (
	"net/http"
	"slices"

	"github.com/codecolors/buddyget/internal/alerts"
	"github.com/codecolors/buddyget/internal/budget"
	"github.com/codecolors/buddyget/internal/forecast"
	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/platform/money"
)

// trendMonths est la profondeur de la tendance budget / réel.
const trendMonths = 12

// envelope est une enveloppe telle qu'affichée : état et remplissage de sa
// jauge (0 à 100).
type envelope struct {
	budget.Status
	State budget.Level
	Fill  int
}

// envelopes prépare l'affichage des enveloppes d'un mois, avec le seuil
// d'alerte réglé pour les enveloppes.
func (s *Server) envelopes(r *http.Request, day calendar.Date) ([]envelope, error) {
	statuses, err := s.budgets.Month(r.Context(), day)
	if err != nil || len(statuses) == 0 {
		return nil, err
	}
	risk, err := s.alerts.Setting(r.Context(), alerts.BudgetKind)
	if err != nil {
		return nil, err
	}
	out := make([]envelope, len(statuses))
	for i, st := range statuses {
		out[i] = envelope{Status: st, State: st.Level(risk.Param), Fill: min(st.Percent(), 100)}
	}
	return out, nil
}

// budgetsView est la donnée de la page des budgets.
type budgetsView struct {
	Month           calendar.Date // premier jour du mois affiché
	Prev, Next      calendar.Date
	Current         bool
	Envelopes       []envelope
	Available       int64
	Spent           int64
	Trend           *barChart
	TrendRows       []forecast.Point
	MonthlyBudgeted int64
}

func (s *Server) budgetsPage(w http.ResponseWriter, r *http.Request) {
	today := calendar.Today()
	month := today
	if m := r.FormValue("mois"); m != "" {
		if d, err := calendar.Parse(m + "-01"); err == nil {
			month = d
		}
	}
	t := month.Time()
	v := budgetsView{Month: calendar.FromTime(t.AddDate(0, 0, 1-t.Day()))}
	v.Prev, v.Next = calendar.FromTime(v.Month.Time().AddDate(0, -1, 0)), calendar.FromTime(v.Month.Time().AddDate(0, 1, 0))
	v.Current = today >= v.Month && today < v.Next
	var err error
	if v.Envelopes, err = s.envelopes(r, v.Month); err != nil {
		s.serverError(w, r, err)
		return
	}
	for _, e := range v.Envelopes {
		v.Available += e.Available
		v.Spent += e.Spent
	}
	if len(v.Envelopes) > 0 {
		if err := s.budgetTrend(r, &v, today); err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	s.render(w, r, http.StatusOK, "budgets", "Budgets", v)
}

func (s *Server) budgetTrend(r *http.Request, v *budgetsView, today calendar.Date) error {
	history, err := s.budgets.History(r.Context(), today, trendMonths)
	if err != nil {
		return err
	}
	budgets, err := s.budgets.Budgets(r.Context())
	if err != nil {
		return err
	}
	v.MonthlyBudgeted = budget.Total(budgets)
	for _, h := range history {
		v.TrendRows = append(v.TrendRows, forecast.Point{Date: h.Month, Balance: h.Spent})
	}
	c := newBarChart("tendance", v.TrendRows, v.MonthlyBudgeted)
	c.Desc = "Dépenses des catégories budgétées, mois par mois, face au total mensuel des enveloppes (" + money.Format(v.MonthlyBudgeted) + "). Le détail suit le graphique."
	v.Trend = &c
	return nil
}

// budgetForm est la donnée du formulaire d'enveloppe.
type budgetForm struct {
	CategoryID int64
	Existing   bool
	Amount     string
	CarryOver  bool
	Error      string
	Categories []ledger.Category // catégories de dépenses
}

func (s *Server) renderBudgetForm(w http.ResponseWriter, r *http.Request, status int, f budgetForm) {
	cats, err := s.ledger.Categories(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	f.Categories = slices.DeleteFunc(cats, func(c ledger.Category) bool { return c.Kind != ledger.CategoryExpense })
	title := "Nouvelle enveloppe"
	if f.Existing {
		title = "Modifier l'enveloppe"
	}
	s.render(w, r, status, "budget_form", title, f)
}

func (s *Server) newBudgetPage(w http.ResponseWriter, r *http.Request) {
	s.renderBudgetForm(w, r, http.StatusOK, budgetForm{CategoryID: formID(r, "categorie")})
}

func (s *Server) editBudgetPage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	budgets, err := s.budgets.Budgets(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	i := slices.IndexFunc(budgets, func(b budget.Budget) bool { return b.CategoryID == id })
	if i < 0 {
		s.notFound(w, r)
		return
	}
	b := budgets[i]
	s.renderBudgetForm(w, r, http.StatusOK, budgetForm{CategoryID: id, Existing: true, Amount: money.FormatInput(b.Amount), CarryOver: b.CarryOver})
}

func (s *Server) saveBudget(w http.ResponseWriter, r *http.Request) {
	if parseForm(w, r) != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	f := budgetForm{
		CategoryID: formID(r, "category"), Existing: r.PostFormValue("existing") != "",
		Amount: r.PostFormValue("amount"), CarryOver: r.PostFormValue("carry_over") != "",
	}
	err := func() error {
		amount, err := money.Parse(f.Amount)
		if err != nil {
			return err
		}
		return s.budgets.Save(r.Context(), budget.Input{CategoryID: f.CategoryID, Amount: amount, CarryOver: f.CarryOver}, calendar.Today())
	}()
	if err != nil {
		if msg, ok := s.inputError(w, r, err); ok {
			f.Error = msg
			s.renderBudgetForm(w, r, http.StatusUnprocessableEntity, f)
		}
		return
	}
	s.redirect(w, r, "/budgets", "Enveloppe enregistrée.")
}

func (s *Server) deleteBudget(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	if err := s.budgets.Delete(r.Context(), id); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.redirect(w, r, "/budgets", "Enveloppe supprimée.")
}
