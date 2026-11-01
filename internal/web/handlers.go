package web

import (
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/codecolors/buddyget/internal/auth"
	"github.com/codecolors/buddyget/internal/platform/validation"
)

// credentialsForm est la donnée des pages de connexion et de création du
// premier compte. Le mot de passe n'est jamais renvoyé dans la page.
type credentialsForm struct {
	Username       string
	Error          string
	PasswordMinLen int
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if err := s.db.R.QueryRowContext(r.Context(), "SELECT 1").Scan(new(int)); err != nil {
		slog.ErrorContext(r.Context(), "healthz : base indisponible", "err", err)
		http.Error(w, "base indisponible", http.StatusServiceUnavailable)
		return
	}
	_, _ = w.Write([]byte("ok\n"))
}

// redirectSetup aiguille vers /setup tant qu'aucun compte n'existe. Elle
// renvoie true si la réponse est déjà écrite.
func (s *Server) redirectSetup(w http.ResponseWriter, r *http.Request) bool {
	need, err := s.auth.NeedsSetup(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return true
	}
	if need {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
	}
	return need
}

// redirectIfConfigured renvoie vers /login une fois le premier compte créé.
func (s *Server) redirectIfConfigured(w http.ResponseWriter, r *http.Request) bool {
	need, err := s.auth.NeedsSetup(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return true
	}
	if !need {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	}
	return !need
}

func (s *Server) setupPage(w http.ResponseWriter, r *http.Request) {
	if s.redirectIfConfigured(w, r) {
		return
	}
	s.render(w, r, http.StatusOK, "setup", "Création du compte", credentialsForm{PasswordMinLen: auth.PasswordMinLen})
}

func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	if s.redirectIfConfigured(w, r) {
		return
	}
	if err := parseForm(w, r); err != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	form := credentialsForm{Username: r.PostFormValue("username"), PasswordMinLen: auth.PasswordMinLen}
	password := r.PostFormValue("password")
	if password != r.PostFormValue("confirm") {
		form.Error = "Les deux mots de passe ne correspondent pas."
		s.render(w, r, http.StatusUnprocessableEntity, "setup", "Création du compte", form)
		return
	}

	u, err := s.auth.CreateFirstUser(r.Context(), form.Username, password)
	if msg, ok := validation.Message(err); ok {
		form.Error = msg
		s.render(w, r, http.StatusUnprocessableEntity, "setup", "Création du compte", form)
		return
	}
	switch {
	case errors.Is(err, auth.ErrSetupDone):
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	}
	slog.InfoContext(r.Context(), "premier compte créé", "utilisateur", u.ID)
	s.startSession(w, r, u)
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	if s.redirectSetup(w, r) {
		return
	}
	if _, err := s.session(r); err == nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.render(w, r, http.StatusOK, "login", "Connexion", credentialsForm{})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if s.redirectSetup(w, r) {
		return
	}
	if err := parseForm(w, r); err != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	form := credentialsForm{Username: r.PostFormValue("username")}
	ip := clientIP(r, s.trustedProxies)

	u, err := s.auth.Authenticate(r.Context(), ip, form.Username, r.PostFormValue("password"))
	var throttled *auth.ThrottledError
	switch {
	case errors.As(err, &throttled):
		minutes := int(math.Ceil(throttled.RetryAfter.Minutes()))
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(throttled.RetryAfter.Seconds()))))
		form.Error = fmt.Sprintf("Trop de tentatives de connexion. Réessayez dans %d min.", minutes)
		s.render(w, r, http.StatusTooManyRequests, "login", "Connexion", form)
		return
	case errors.Is(err, auth.ErrInvalidCredentials):
		slog.WarnContext(r.Context(), "échec de connexion", "ip", ip)
		form.Error = "Identifiant ou mot de passe incorrect."
		s.render(w, r, http.StatusUnauthorized, "login", "Connexion", form)
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	}
	s.startSession(w, r, u)
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, u auth.User) {
	sess, err := s.auth.CreateSession(r.Context(), u)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.setSessionCookie(w, sess)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if err := s.auth.DeleteSession(r.Context(), c.Value); err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	s.clearSessionCookie(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) setTheme(w http.ResponseWriter, r *http.Request) {
	if err := parseForm(w, r); err != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	theme, ok := auth.ParseTheme(r.PostFormValue("theme"))
	if !ok {
		s.renderError(w, r, http.StatusBadRequest, "Thème inconnu.")
		return
	}
	if err := s.auth.SetTheme(r.Context(), currentUser(r.Context()).ID, theme); err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, localPath(r.PostFormValue("back")), http.StatusSeeOther)
}

// localPath n'accepte qu'un chemin interne à l'application, pour éviter les
// redirections ouvertes vers un autre site.
func localPath(p string) string {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.HasPrefix(p, "/\\") {
		return "/"
	}
	return p
}
