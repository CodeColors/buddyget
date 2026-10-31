package web

import (
	"net/http"
	"strconv"

	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/platform/calendar"
)

// useTemplate crée l'opération du jour d'un modèle de saisie.
func (s *Server) useTemplate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	created, err := s.ledger.UseTemplate(r.Context(), id, calendar.Today())
	if err != nil {
		s.failed(w, r, err, "/operations/nouvelle")
		return
	}
	t, err := s.ledger.Transaction(r.Context(), created)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.redirect(w, r, "/operations", "Opération « "+t.Label+" » enregistrée.")
}

// saveTemplate fait d'une opération un modèle de saisie.
func (s *Server) saveTemplate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok || parseForm(w, r) != nil {
		s.notFound(w, r)
		return
	}
	back := "/operations/" + strconv.FormatInt(id, 10)
	if _, err := s.ledger.SaveAsTemplate(r.Context(), id, r.PostFormValue("name")); err != nil {
		s.failed(w, r, err, back)
		return
	}
	s.redirect(w, r, back, "Modèle enregistré : il est proposé à la saisie d'une opération.")
}

func (s *Server) deleteTemplate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	if err := s.ledger.DeleteTemplate(r.Context(), id); err != nil {
		s.failed(w, r, err, "/operations/nouvelle")
		return
	}
	s.redirect(w, r, "/operations/nouvelle", "Modèle supprimé.")
}

// pointageRow est une ligne de la vue de pointage, avec l'état du
// rapprochement de son compte (mis à jour en même temps par HTMX).
type pointageRow struct {
	T    ledger.Transaction
	R    ledger.Reconciliation
	Back string
}

// toggleCleared pointe ou dépointe une opération. Une requête HTMX reçoit la
// ligne mise à jour et le nouvel écart ; sinon, retour à la page d'origine.
func (s *Server) toggleCleared(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok || parseForm(w, r) != nil {
		s.notFound(w, r)
		return
	}
	back := localPath(r.PostFormValue("back"))
	cleared := r.PostFormValue("cleared") == "1"
	if err := s.ledger.SetCleared(r.Context(), id, cleared); err != nil {
		s.failed(w, r, err, back)
		return
	}
	if r.Header.Get("HX-Request") != "true" {
		msg := "Opération dépointée."
		if cleared {
			msg = "Opération pointée."
		}
		s.redirect(w, r, back, msg)
		return
	}
	t, err := s.ledger.Transaction(r.Context(), id)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	rec, err := s.ledger.Reconciliation(r.Context(), t.AccountID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.renderBlock(w, r, "reconcile", "pointage-update", pointageRow{T: t, R: rec, Back: back})
}
