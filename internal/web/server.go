// Package web expose l'interface HTTP : routage, middlewares, pages rendues
// côté serveur et fichiers statiques embarqués. Les handlers ne contiennent
// aucune logique métier : ils délèguent aux packages du domaine.
package web

import (
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/codecolors/buddyget/internal/auth"
	"github.com/codecolors/buddyget/internal/config"
	"github.com/codecolors/buddyget/internal/importer"
	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/platform/db"
	"github.com/codecolors/buddyget/internal/platform/money"
	"github.com/codecolors/buddyget/internal/platform/validation"
)

const (
	sessionCookie = "buddyget_session"
	flashCookie   = "buddyget_flash"
)

// maxFormBytes borne la taille des formulaires (hors futurs envois de
// fichiers, qui auront leur propre limite).
const maxFormBytes = 64 << 10

// Server regroupe les dépendances des handlers.
type Server struct {
	db             *db.DB
	auth           *auth.Service
	ledger         *ledger.Service
	importer       *importer.Service
	maxUpload      int64
	pages          *renderer
	assets         assets
	secureCookies  bool
	trustedProxies []netip.Prefix
}

// New construit le gestionnaire HTTP complet de l'application.
func New(cfg config.Config, database *db.DB, authService *auth.Service, ledgerService *ledger.Service, importService *importer.Service) (http.Handler, error) {
	static, err := loadAssets()
	if err != nil {
		return nil, err
	}
	pages, err := newRenderer(template.FuncMap{
		"asset":        static.url,
		"euros":        money.Format,
		"eurosSigned":  money.FormatSigned,
		"amountInput":  money.FormatInput,
		"navActive":    navActive,
		"plural":       plural,
		"datetime":     func(t time.Time) string { return t.In(time.Local).Format("02/01/2006 à 15h04") },
		"columnLetter": columnLetter,
		"columnChoice": columnChoice,
	})
	if err != nil {
		return nil, err
	}
	s := &Server{
		db:             database,
		auth:           authService,
		ledger:         ledgerService,
		importer:       importService,
		maxUpload:      cfg.MaxUploadBytes(),
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
	for pattern, h := range map[string]http.HandlerFunc{
		"GET /{$}":                             s.dashboard,
		"POST /preferences/theme":              s.setTheme,
		"GET /operations":                      s.operationsPage,
		"GET /operations/nouvelle":             s.newOperationPage,
		"POST /operations":                     s.createOperation,
		"GET /operations/{id}":                 s.editOperationPage,
		"POST /operations/{id}":                s.updateOperation,
		"POST /operations/{id}/supprimer":      s.deleteOperation,
		"POST /operations/lot":                 s.batchOperations,
		"GET /comptes":                         s.accountsPage,
		"GET /comptes/nouveau":                 s.newAccountPage,
		"POST /comptes":                        s.createAccount,
		"GET /comptes/{id}":                    s.editAccountPage,
		"POST /comptes/{id}":                   s.updateAccount,
		"POST /comptes/{id}/archiver":          s.archiveAccount,
		"POST /comptes/{id}/supprimer":         s.deleteAccount,
		"GET /categories":                      s.categoriesPage,
		"GET /categories/nouvelle":             s.newCategoryPage,
		"POST /categories":                     s.createCategory,
		"GET /categories/{id}":                 s.editCategoryPage,
		"POST /categories/{id}":                s.updateCategory,
		"POST /categories/{id}/supprimer":      s.deleteCategory,
		"GET /comptes/{id}/rapprochement":      s.reconcilePage,
		"POST /comptes/{id}/rapprochement":     s.saveReconcile,
		"GET /import":                          s.importPage,
		"POST /import":                         s.uploadImport,
		"GET /import/historique":               s.importHistory,
		"POST /import/historique/{id}/annuler": s.undoImport,
		"GET /import/{token}":                  s.draftPage,
		"GET /import/{token}/colonnes":         s.mappingPage,
		"POST /import/{token}/colonnes":        s.saveMapping,
		"POST /import/{token}/selection":       s.selectLines,
		"POST /import/{token}/valider":         s.commitImport,
		"POST /import/{token}/abandonner":      s.cancelImport,
	} {
		mux.Handle(pattern, s.requireUser(h))
	}
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

// render affiche une page dans la mise en page commune, avec le message
// de confirmation éventuellement laissé par l'action précédente.
func (s *Server) render(w http.ResponseWriter, r *http.Request, status int, page, title string, data any) {
	v := view{Title: title, Path: r.URL.Path, Theme: auth.ThemeAuto, User: currentUser(r.Context()), Data: data}
	if v.User != nil {
		v.Theme = v.User.Theme
		v.Flash = s.popFlash(w, r)
	}
	s.execute(w, r, status, page, "layout", v)
}

// renderBlock n'affiche qu'un bloc de la page (réponse à une requête HTMX).
func (s *Server) renderBlock(w http.ResponseWriter, r *http.Request, page, block string, data any) {
	s.execute(w, r, http.StatusOK, page, block, view{Path: r.URL.Path, User: currentUser(r.Context()), Data: data})
}

func (s *Server) execute(w http.ResponseWriter, r *http.Request, status int, page, block string, v view) {
	if err := s.pages.render(w, status, page, block, v); err != nil {
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

// inputError renvoie le message d'une erreur de saisie, à réafficher dans le
// formulaire. Toute autre erreur reçoit ici sa réponse (404 ou 500) et ok
// vaut false.
func (s *Server) inputError(w http.ResponseWriter, r *http.Request, err error) (msg string, ok bool) {
	if msg, ok := validation.Message(err); ok {
		return msg, true
	}
	if errors.Is(err, ledger.ErrNotFound) {
		s.notFound(w, r)
	} else {
		s.serverError(w, r, err)
	}
	return "", false
}

// failed traite une erreur sans formulaire à réafficher : une saisie
// refusée devient un message de confirmation sur la page de retour.
func (s *Server) failed(w http.ResponseWriter, r *http.Request, err error, back string) {
	if msg, ok := s.inputError(w, r, err); ok {
		s.redirect(w, r, back, msg)
	}
}

// redirect renvoie vers une page de l'application, avec un message
// facultatif affiché à l'arrivée.
func (s *Server) redirect(w http.ResponseWriter, r *http.Request, to, flash string) {
	if flash != "" {
		s.writeCookie(w, flashCookie, url.QueryEscape(flash), time.Now().Add(time.Minute))
	}
	http.Redirect(w, r, localPath(to), http.StatusSeeOther)
}

func (s *Server) popFlash(w http.ResponseWriter, r *http.Request) string {
	c, err := r.Cookie(flashCookie)
	if err != nil {
		return ""
	}
	s.writeCookie(w, flashCookie, "", time.Time{})
	msg, _ := url.QueryUnescape(c.Value)
	return msg
}

func (s *Server) session(r *http.Request) (auth.Session, error) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return auth.Session{}, auth.ErrNoSession
	}
	return s.auth.Session(r.Context(), c.Value)
}

func (s *Server) setSessionCookie(w http.ResponseWriter, sess auth.Session) {
	s.writeCookie(w, sessionCookie, sess.Token, sess.ExpiresAt)
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	s.writeCookie(w, sessionCookie, "", time.Time{})
}

// writeCookie est l'unique point d'écriture des cookies ; une valeur vide
// efface le cookie. Secure suit BASE_URL : l'accès en HTTP simple sur le
// réseau local doit rester possible.
func (s *Server) writeCookie(w http.ResponseWriter, name, value string, expires time.Time) {
	c := &http.Cookie{ //nolint:gosec // G124 : Secure configurable, voir ci-dessus.
		Name:     name,
		Value:    value,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   s.secureCookies,
		SameSite: http.SameSiteLaxMode,
	}
	if value == "" {
		c.MaxAge = -1
	}
	http.SetCookie(w, c)
}

// parseForm lit un formulaire en bornant sa taille.
func parseForm(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	return r.ParseForm()
}

// pathID lit l'identifiant numérique {id} du chemin.
func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

// formID lit un identifiant facultatif d'un champ de formulaire (0 si vide).
func formID(r *http.Request, field string) int64 {
	id, _ := strconv.ParseInt(r.FormValue(field), 10, 64)
	return max(id, 0)
}

// navActive indique si le chemin courant relève d'une rubrique.
func navActive(path, section string) bool {
	if section == "/" {
		return path == "/"
	}
	return path == section || strings.HasPrefix(path, section+"/")
}

// plural accorde un nom à la française : « 0 opération », « 3 opérations ».
func plural(n int, one, many string) string {
	if n <= 1 {
		return strconv.Itoa(n) + " " + one
	}
	return strconv.Itoa(n) + " " + many
}
