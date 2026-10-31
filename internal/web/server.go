// Package web expose l'interface HTTP : routage, middlewares, pages rendues
// côté serveur et fichiers statiques embarqués. Les handlers ne contiennent
// aucune logique métier : ils délèguent aux packages du domaine.
package web

import (
	"html/template"
	"log/slog"
	"net/http"
	"net/netip"
	"time"

	"github.com/codecolors/buddyget/internal/auth"
	"github.com/codecolors/buddyget/internal/config"
	"github.com/codecolors/buddyget/internal/platform/db"
)

const sessionCookie = "buddyget_session"

// maxFormBytes borne la taille des formulaires (hors futurs envois de
// fichiers, qui auront leur propre limite).
const maxFormBytes = 64 << 10

// Server regroupe les dépendances des handlers.
type Server struct {
	db             *db.DB
	auth           *auth.Service
	pages          *renderer
	assets         assets
	secureCookies  bool
	trustedProxies []netip.Prefix
}

// New construit le gestionnaire HTTP complet de l'application.
func New(cfg config.Config, database *db.DB, authService *auth.Service) (http.Handler, error) {
	static, err := loadAssets()
	if err != nil {
		return nil, err
	}
	pages, err := newRenderer(template.FuncMap{"asset": static.url})
	if err != nil {
		return nil, err
	}
	s := &Server{
		db:             database,
		auth:           authService,
		pages:          pages,
		assets:         static,
		secureCookies:  cfg.SecureCookies(),
		trustedProxies: cfg.TrustedProxies,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.Handle("GET /static/{file}", s.assets)
	mux.HandleFunc("GET /setup", s.setupPage)
	mux.HandleFunc("POST /setup", s.setup)
	mux.HandleFunc("GET /login", s.loginPage)
	mux.HandleFunc("POST /login", s.login)
	mux.HandleFunc("POST /logout", s.logout)
	mux.Handle("GET /{$}", s.requireUser(s.dashboard))
	mux.Handle("POST /preferences/theme", s.requireUser(s.setTheme))
	mux.HandleFunc("/", s.notFound)

	// Protection CSRF : la bibliothèque standard refuse toute requête non
	// sûre émise depuis une autre origine (en-têtes Sec-Fetch-Site / Origin).
	csrf := http.NewCrossOriginProtection()
	if cfg.BaseURL != nil {
		if err := csrf.AddTrustedOrigin(cfg.BaseURL.Scheme + "://" + cfg.BaseURL.Host); err != nil {
			return nil, err
		}
	}
	csrf.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.renderError(w, r, http.StatusForbidden, "Requête refusée : elle ne provient pas de BuddyGet.")
	}))

	return securityHeaders(logRequests(csrf.Handler(mux))), nil
}

// render affiche une page dans la mise en page commune.
func (s *Server) render(w http.ResponseWriter, r *http.Request, status int, page, title string, data any) {
	v := view{Title: title, Path: r.URL.Path, Theme: auth.ThemeAuto, User: currentUser(r.Context()), Data: data}
	if v.User != nil {
		v.Theme = v.User.Theme
	}
	if err := s.pages.render(w, status, page, v); err != nil {
		slog.ErrorContext(r.Context(), "rendu de page", "page", page, "err", err)
		http.Error(w, "Erreur interne", http.StatusInternalServerError)
	}
}

var errorTitles = map[int]string{
	http.StatusBadRequest:          "Requête invalide",
	http.StatusForbidden:           "Accès refusé",
	http.StatusNotFound:            "Page introuvable",
	http.StatusInternalServerError: "Erreur interne",
}

func (s *Server) renderError(w http.ResponseWriter, r *http.Request, status int, message string) {
	s.render(w, r, status, "error", errorTitles[status], message)
}

func (s *Server) serverError(w http.ResponseWriter, r *http.Request, err error) {
	slog.ErrorContext(r.Context(), "erreur interne", "chemin", r.URL.Path, "err", err)
	s.renderError(w, r, http.StatusInternalServerError, "Une erreur interne est survenue. Réessayez dans un instant.")
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	s.renderError(w, r, http.StatusNotFound, "Cette page n'existe pas.")
}

func (s *Server) session(r *http.Request) (auth.Session, error) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return auth.Session{}, auth.ErrNoSession
	}
	return s.auth.Session(r.Context(), c.Value)
}

func (s *Server) setSessionCookie(w http.ResponseWriter, sess auth.Session) {
	s.writeSessionCookie(w, sess.Token, sess.ExpiresAt)
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	s.writeSessionCookie(w, "", time.Time{})
}

// writeSessionCookie est l'unique point d'écriture du cookie de session ; un
// jeton vide l'efface. Secure suit BASE_URL : l'accès en HTTP simple sur le
// réseau local doit rester possible.
func (s *Server) writeSessionCookie(w http.ResponseWriter, token string, expires time.Time) {
	c := &http.Cookie{ //nolint:gosec // G124 : Secure configurable, voir ci-dessus.
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   s.secureCookies,
		SameSite: http.SameSiteLaxMode,
	}
	if token == "" {
		c.MaxAge = -1
	}
	http.SetCookie(w, c)
}

// parseForm lit un formulaire en bornant sa taille.
func parseForm(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	return r.ParseForm()
}
