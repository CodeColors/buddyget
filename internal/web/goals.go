package web

import (
	"net/http"

	"github.com/codecolors/buddyget/internal/goals"
	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/platform/money"
)

// goalView est un projet tel qu'affiché, calculs du jour compris.
type goalView struct {
	goals.Goal
	Suggested int64
	Projected calendar.Date // vide : pas de versement programmé
	Late      bool
}

// goalAccount résume un compte d'épargne portant des projets.
type goalAccount struct {
	ledger.AccountBalance
	Unallocated int64
}

// goalsView est la donnée de la page des projets.
type goalsView struct {
	Goals    []goalView
	Accounts []goalAccount
	Sources  []ledger.AccountBalance // comptes d'où programmer un versement
}

func (s *Server) goalsPage(w http.ResponseWriter, r *http.Request) {
	ctx, today := r.Context(), calendar.Today()
	list, err := s.goals.Goals(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	accounts, err := s.ledger.Accounts(ctx, today)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	var v goalsView
	used := map[int64]bool{}
	for _, g := range list {
		gv := goalView{Goal: g, Suggested: g.Suggested(today), Late: g.Late(today)}
		if d, ok := g.Projected(today); ok {
			gv.Projected = d
		}
		v.Goals = append(v.Goals, gv)
		used[g.AccountID] = true
	}
	for _, a := range accounts {
		if used[a.ID] {
			free, err := s.goals.Unallocated(ctx, a.ID, today)
			if err != nil {
				s.serverError(w, r, err)
				return
			}
			v.Accounts = append(v.Accounts, goalAccount{AccountBalance: a, Unallocated: free})
		}
		if !a.Archived && a.Kind == ledger.AccountCurrent {
			v.Sources = append(v.Sources, a)
		}
	}
	s.render(w, r, http.StatusOK, "goals", "Projets", v)
}

// allocateGoal ajoute à la cagnotte d'un projet, ou en retire.
func (s *Server) allocateGoal(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	if parseForm(w, r) != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	amount, err := money.Parse(r.PostFormValue("amount"))
	if err == nil {
		amount = max(amount, -amount)
		if r.PostFormValue("sens") == "retirer" {
			amount = -amount
		}
		err = s.goals.Allocate(r.Context(), id, amount, calendar.Today())
	}
	if err != nil {
		s.failed(w, r, err, "/projets")
		return
	}
	s.redirect(w, r, "/projets", "Cagnotte mise à jour.")
}

// scheduleGoal crée le versement mensuel suggéré vers le compte du projet.
func (s *Server) scheduleGoal(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	if parseForm(w, r) != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	if err := s.goals.Schedule(r.Context(), id, formID(r, "from"), calendar.Today()); err != nil {
		s.failed(w, r, err, "/projets")
		return
	}
	s.redirect(w, r, "/projets", "Versement programmé créé : il entre dans le prévisionnel.")
}

// goalForm est la donnée du formulaire de projet, en texte saisi.
type goalForm struct {
	ID         int64
	Name       string
	Target     string
	TargetDate string
	AccountID  int64
	Tag        string
	Error      string
	Accounts   []ledger.AccountBalance // comptes d'épargne
}

func (s *Server) renderGoalForm(w http.ResponseWriter, r *http.Request, status int, f goalForm) {
	accounts, err := s.ledger.Accounts(r.Context(), calendar.Today())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	for _, a := range accounts {
		if a.Kind == ledger.AccountSavings && (!a.Archived || a.ID == f.AccountID) {
			f.Accounts = append(f.Accounts, a)
		}
	}
	title := "Nouveau projet"
	if f.ID != 0 {
		title = "Modifier le projet"
	}
	s.render(w, r, status, "goal_form", title, f)
}

func (s *Server) newGoalPage(w http.ResponseWriter, r *http.Request) {
	s.renderGoalForm(w, r, http.StatusOK, goalForm{})
}

func (s *Server) editGoalPage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	g, err := s.goals.Goal(r.Context(), id)
	if err != nil {
		s.inputError(w, r, err)
		return
	}
	s.renderGoalForm(w, r, http.StatusOK, goalForm{
		ID: id, Name: g.Name, Target: money.FormatInput(g.Target), TargetDate: string(g.TargetDate), AccountID: g.AccountID, Tag: g.Tag,
	})
}

func (s *Server) createGoal(w http.ResponseWriter, r *http.Request) { s.saveGoal(w, r, 0) }

func (s *Server) updateGoal(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	s.saveGoal(w, r, id)
}

func (s *Server) saveGoal(w http.ResponseWriter, r *http.Request, id int64) {
	if parseForm(w, r) != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	f := goalForm{
		ID: id, Name: r.PostFormValue("name"), Target: r.PostFormValue("target"), TargetDate: r.PostFormValue("target_date"),
		AccountID: formID(r, "account"), Tag: r.PostFormValue("tag"),
	}
	err := func() error {
		target, err := money.Parse(f.Target)
		if err != nil {
			return err
		}
		date, err := calendar.Parse(f.TargetDate)
		if err != nil {
			return err
		}
		in := goals.Input{Name: f.Name, Target: target, TargetDate: date, AccountID: f.AccountID, Tag: f.Tag}
		if id == 0 {
			_, err = s.goals.Create(r.Context(), in)
			return err
		}
		return s.goals.Update(r.Context(), id, in)
	}()
	if err != nil {
		if msg, ok := s.inputError(w, r, err); ok {
			f.Error = msg
			s.renderGoalForm(w, r, http.StatusUnprocessableEntity, f)
		}
		return
	}
	s.redirect(w, r, "/projets", "Projet enregistré.")
}

func (s *Server) deleteGoal(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	if err := s.goals.Delete(r.Context(), id); err != nil {
		s.failed(w, r, err, "/projets")
		return
	}
	s.redirect(w, r, "/projets", "Projet supprimé ; son versement programmé reste dans les récurrences.")
}
