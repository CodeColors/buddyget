package web

import (
	"net/http"

	"github.com/codecolors/buddyget/internal/auth"
)

// settingsPage regroupe les réglages du foyer : comptes, utilisateurs,
// membres, notifications, sauvegardes.
func (s *Server) settingsPage(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "settings", "Paramètres", nil)
}

// usersView est la donnée de la page des utilisateurs.
type usersView struct {
	Users       []auth.User
	Me          int64
	Username    string // saisie refusée d'un nouveau compte
	AddError    string // erreur du formulaire d'ajout
	ChangeError string // erreur du changement de mot de passe
}

func (s *Server) usersPage(w http.ResponseWriter, r *http.Request) {
	s.renderUsers(w, r, http.StatusOK, usersView{})
}

func (s *Server) renderUsers(w http.ResponseWriter, r *http.Request, status int, v usersView) {
	var err error
	if v.Users, err = s.auth.Users(r.Context()); err != nil {
		s.serverError(w, r, err)
		return
	}
	v.Me = currentUser(r.Context()).ID
	s.render(w, r, status, "users", "Utilisateurs", v)
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	if parseForm(w, r) != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	username := r.PostFormValue("username")
	if r.PostFormValue("password") != r.PostFormValue("confirm") {
		s.renderUsers(w, r, http.StatusUnprocessableEntity, usersView{Username: username, AddError: "Les deux mots de passe diffèrent."})
		return
	}
	if _, err := s.auth.CreateUser(r.Context(), username, r.PostFormValue("password")); err != nil {
		if msg, ok := s.inputError(w, r, err); ok {
			s.renderUsers(w, r, http.StatusUnprocessableEntity, usersView{Username: username, AddError: msg})
		}
		return
	}
	s.redirect(w, r, "/utilisateurs", "Compte créé : transmettez son identifiant et son mot de passe à son titulaire.")
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	if parseForm(w, r) != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	next := r.PostFormValue("new_password")
	if next != r.PostFormValue("confirm") {
		s.renderUsers(w, r, http.StatusUnprocessableEntity, usersView{ChangeError: "Les deux nouveaux mots de passe diffèrent."})
		return
	}
	var token string
	if c, err := r.Cookie(sessionCookie); err == nil {
		token = c.Value
	}
	err := s.auth.ChangePassword(r.Context(), currentUser(r.Context()).ID, r.PostFormValue("current"), next, token)
	if err != nil {
		if msg, ok := s.inputError(w, r, err); ok {
			s.renderUsers(w, r, http.StatusUnprocessableEntity, usersView{ChangeError: msg})
		}
		return
	}
	s.redirect(w, r, "/utilisateurs", "Mot de passe changé ; vos autres sessions sont fermées.")
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	if err := s.auth.DeleteUser(r.Context(), id, currentUser(r.Context()).ID); err != nil {
		s.failed(w, r, err, "/utilisateurs")
		return
	}
	s.redirect(w, r, "/utilisateurs", "Compte supprimé.")
}
