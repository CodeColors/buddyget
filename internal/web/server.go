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

	"github.com/codecolors/buddyget/internal/alerts"
	"github.com/codecolors/buddyget/internal/attachments"
	"github.com/codecolors/buddyget/internal/auth"
	"github.com/codecolors/buddyget/internal/backup"
	"github.com/codecolors/buddyget/internal/budget"
	"github.com/codecolors/buddyget/internal/categorize"
	"github.com/codecolors/buddyget/internal/config"
	"github.com/codecolors/buddyget/internal/exports"
	"github.com/codecolors/buddyget/internal/forecast"
	"github.com/codecolors/buddyget/internal/goals"
	"github.com/codecolors/buddyget/internal/household"
	"github.com/codecolors/buddyget/internal/importer"
	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/loans"
	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/platform/db"
	"github.com/codecolors/buddyget/internal/platform/money"
	"github.com/codecolors/buddyget/internal/platform/validation"
	"github.com/codecolors/buddyget/internal/recurring"
	"github.com/codecolors/buddyget/internal/reports"
	"github.com/codecolors/buddyget/internal/savings"
	"github.com/codecolors/buddyget/internal/scenarios"
)

const (
	sessionCookie = "buddyget_session"
	flashCookie   = "buddyget_flash"
)

// maxFormBytes borne la taille des formulaires (hors futurs envois de
// fichiers, qui auront leur propre limite).
const maxFormBytes = 64 << 10

// Services regroupe les services du domaine utilisés par l'interface.
type Services struct {
	Auth        *auth.Service
	Ledger      *ledger.Service
	Rules       *categorize.Service
	Importer    *importer.Service
	Recurring   *recurring.Service
	Savings     *savings.Service
	Budgets     *budget.Service
	Forecast    *forecast.Service
	Alerts      *alerts.Service
	Reports     *reports.Service
	Attachments *attachments.Service
	Loans       *loans.Service
	Goals       *goals.Service
	Scenarios   *scenarios.Service
	Household   *household.Service
	Exports     *exports.Service
	Backups     *backup.Service
	// Restart arrête proprement BuddyGet, que la politique de redémarrage
	// du conteneur relance (restauration d'une sauvegarde).
	Restart func()
}

// Server regroupe les dépendances des handlers.
type Server struct {
	db             *db.DB
	auth           *auth.Service
	ledger         *ledger.Service
	rules          *categorize.Service
	importer       *importer.Service
	recurring      *recurring.Service
	savings        *savings.Service
	budgets        *budget.Service
	forecast       *forecast.Service
	alerts         *alerts.Service
	reports        *reports.Service
	attachments    *attachments.Service
	loans          *loans.Service
	goals          *goals.Service
	scenarios      *scenarios.Service
	household      *household.Service
	exports        *exports.Service
	backups        *backup.Service
	restart        func()
	maxUpload      int64
	pages          *renderer
	assets         assets
	secureCookies  bool
	trustedProxies []netip.Prefix
}

// New construit le gestionnaire HTTP complet de l'application.
func New(cfg config.Config, database *db.DB, svc Services) (http.Handler, error) {
	static, err := loadAssets()
	if err != nil {
		return nil, err
	}
	pages, err := newRenderer(template.FuncMap{
		"asset":        static.url,
		"euros":        money.Format,
		"eurosSigned":  money.FormatSigned,
		"amountInput":  money.FormatInput,
		"neg":          func(v int64) int64 { return -v },
		"add":          func(a, b int64) int64 { return a + b },
		"sub":          func(a, b int64) int64 { return a - b },
		"yearMonth":    func(d calendar.Date) string { return string(d)[:7] },
		"percent":      func(hundredths int) string { return money.FormatInput(int64(hundredths)) + "\u00a0%" },
		"rate":         func(thousandths int) string { return formatRate(thousandths) + "\u00a0%" },
		"fileSize":     fileSize,
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
		auth:           svc.Auth,
		ledger:         svc.Ledger,
		rules:          svc.Rules,
		importer:       svc.Importer,
		recurring:      svc.Recurring,
		savings:        svc.Savings,
		budgets:        svc.Budgets,
		forecast:       svc.Forecast,
		alerts:         svc.Alerts,
		reports:        svc.Reports,
		attachments:    svc.Attachments,
		loans:          svc.Loans,
		goals:          svc.Goals,
		scenarios:      svc.Scenarios,
		household:      svc.Household,
		exports:        svc.Exports,
		backups:        svc.Backups,
		restart:        svc.Restart,
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
		"GET /{$}":                                            s.dashboard,
		"POST /preferences/theme":                             s.setTheme,
		"GET /operations":                                     s.operationsPage,
		"GET /operations/nouvelle":                            s.newOperationPage,
		"POST /operations":                                    s.createOperation,
		"GET /operations/{id}":                                s.editOperationPage,
		"POST /operations/{id}":                               s.updateOperation,
		"POST /operations/{id}/supprimer":                     s.deleteOperation,
		"POST /operations/lot":                                s.batchOperations,
		"GET /comptes":                                        s.accountsPage,
		"GET /comptes/nouveau":                                s.newAccountPage,
		"POST /comptes":                                       s.createAccount,
		"GET /comptes/{id}":                                   s.editAccountPage,
		"POST /comptes/{id}":                                  s.updateAccount,
		"POST /comptes/{id}/archiver":                         s.archiveAccount,
		"POST /comptes/{id}/supprimer":                        s.deleteAccount,
		"GET /categories":                                     s.categoriesPage,
		"GET /categories/nouvelle":                            s.newCategoryPage,
		"POST /categories":                                    s.createCategory,
		"GET /categories/{id}":                                s.editCategoryPage,
		"POST /categories/{id}":                               s.updateCategory,
		"POST /categories/{id}/supprimer":                     s.deleteCategory,
		"GET /comptes/{id}/rapprochement":                     s.reconcilePage,
		"POST /comptes/{id}/rapprochement":                    s.saveReconcile,
		"GET /import":                                         s.importPage,
		"POST /import":                                        s.uploadImport,
		"GET /import/historique":                              s.importHistory,
		"POST /import/historique/{id}/annuler":                s.undoImport,
		"GET /import/{token}":                                 s.draftPage,
		"GET /import/{token}/colonnes":                        s.mappingPage,
		"POST /import/{token}/colonnes":                       s.saveMapping,
		"POST /import/{token}/selection":                      s.selectLines,
		"POST /import/{token}/valider":                        s.commitImport,
		"POST /import/{token}/abandonner":                     s.cancelImport,
		"GET /regles":                                         s.rulesPage,
		"GET /regles/nouvelle":                                s.newRulePage,
		"POST /regles":                                        s.createRule,
		"POST /regles/appliquer":                              s.applyRules,
		"GET /regles/{id}":                                    s.editRulePage,
		"POST /regles/{id}":                                   s.updateRule,
		"POST /regles/{id}/supprimer":                         s.deleteRule,
		"GET /operations/virements":                           s.transfersPage,
		"POST /operations/virements/relier":                   s.linkTransfer,
		"POST /operations/virements/ignorer":                  s.dismissTransfer,
		"POST /operations/{id}/delier":                        s.unlinkTransfer,
		"GET /recurrences":                                    s.recurrencesPage,
		"GET /recurrences/detectees":                          s.detectedPage,
		"POST /recurrences/detectees/valider":                 s.acceptDetected,
		"POST /recurrences/detectees/rejeter":                 s.dismissDetected,
		"GET /recurrences/nouvelle":                           s.newRecurrencePage,
		"POST /recurrences":                                   s.createRecurrence,
		"GET /recurrences/{id}":                               s.editRecurrencePage,
		"POST /recurrences/{id}":                              s.updateRecurrence,
		"POST /recurrences/{id}/activer":                      s.toggleRecurrence,
		"POST /recurrences/{id}/supprimer":                    s.deleteRecurrence,
		"GET /previsionnel":                                   s.forecastPage,
		"POST /previsionnel/exclusions":                       s.excludeOccurrence,
		"POST /previsionnel/reglages":                         s.saveForecastSettings,
		"GET /epargne":                                        s.savingsPage,
		"POST /epargne/{id}/interets":                         s.recordInterest,
		"GET /budgets":                                        s.budgetsPage,
		"GET /budgets/nouveau":                                s.newBudgetPage,
		"GET /budgets/{id}":                                   s.editBudgetPage,
		"POST /budgets":                                       s.saveBudget,
		"POST /budgets/{id}/supprimer":                        s.deleteBudget,
		"GET /alertes":                                        s.alertsPage,
		"POST /alertes/acquitter":                             s.acknowledgeAlert,
		"GET /alertes/reglages":                               s.alertSettingsPage,
		"POST /alertes/reglages":                              s.saveAlertSettings,
		"POST /alertes/canaux":                                s.saveChannels,
		"POST /alertes/essai":                                 s.testChannels,
		"POST /operations/{id}/pointer":                       s.toggleCleared,
		"POST /operations/{id}/modele":                        s.saveTemplate,
		"POST /modeles/{id}/utiliser":                         s.useTemplate,
		"POST /modeles/{id}/supprimer":                        s.deleteTemplate,
		"POST /operations/{id}/pieces":                        s.uploadAttachment,
		"GET /pieces/{id}":                                    s.serveAttachment,
		"POST /pieces/{id}/supprimer":                         s.deleteAttachment,
		"GET /etiquettes":                                     s.tagsPage,
		"POST /etiquettes/{id}":                               s.renameTag,
		"POST /etiquettes/{id}/supprimer":                     s.deleteTag,
		"GET /abonnements":                                    s.subscriptionsPage,
		"GET /rapports":                                       s.reportsPage,
		"GET /credits":                                        s.loansPage,
		"GET /credits/nouveau":                                s.newLoanPage,
		"POST /credits":                                       s.createLoan,
		"GET /credits/{id}":                                   s.loanPage,
		"GET /credits/{id}/modifier":                          s.editLoanPage,
		"POST /credits/{id}":                                  s.updateLoan,
		"POST /credits/{id}/anticipe":                         s.prepayLoan,
		"POST /credits/{id}/supprimer":                        s.deleteLoan,
		"GET /projets":                                        s.goalsPage,
		"GET /projets/nouveau":                                s.newGoalPage,
		"POST /projets":                                       s.createGoal,
		"GET /projets/{id}":                                   s.editGoalPage,
		"POST /projets/{id}":                                  s.updateGoal,
		"POST /projets/{id}/cagnotte":                         s.allocateGoal,
		"POST /projets/{id}/versement":                        s.scheduleGoal,
		"POST /projets/{id}/supprimer":                        s.deleteGoal,
		"GET /scenarios":                                      s.scenariosPage,
		"POST /scenarios":                                     s.createScenario,
		"GET /scenarios/{id}":                                 s.scenarioPage,
		"POST /scenarios/{id}":                                s.renameScenario,
		"POST /scenarios/{id}/modifications":                  s.addScenarioItem,
		"POST /scenarios/{id}/modifications/{item}/supprimer": s.deleteScenarioItem,
		"POST /scenarios/{id}/convertir":                      s.convertScenario,
		"POST /scenarios/{id}/supprimer":                      s.deleteScenario,
		"GET /partage":                                        s.householdPage,
		"POST /partage/membres":                               s.createMember,
		"GET /partage/membres/{id}":                           s.editMemberPage,
		"POST /partage/membres/{id}":                          s.updateMember,
		"POST /partage/membres/{id}/archiver":                 s.archiveMember,
		"POST /partage/membres/{id}/supprimer":                s.deleteMember,
		"POST /partage/remboursements":                        s.settle,
		"POST /partage/remboursements/{id}/supprimer":         s.deleteSettlement,
		"GET /exports":                                        s.exportsPage,
		"GET /exports/operations":                             s.exportOperations,
		"GET /exports/rapport":                                s.exportReport,
		"GET /exports/dossier":                                s.exportDossier,
		"GET /sauvegardes":                                    s.backupsPage,
		"POST /sauvegardes":                                   s.backupNow,
		"POST /sauvegardes/reglages":                          s.saveBackupSettings,
		"POST /sauvegardes/restaurer":                         s.restoreBackup,
		"GET /sauvegardes/fichiers/{name}":                    s.downloadBackup,
		"POST /sauvegardes/fichiers/{name}/supprimer":         s.deleteBackup,
		"GET /parametres":                                     s.settingsPage,
		"GET /utilisateurs":                                   s.usersPage,
		"POST /utilisateurs":                                  s.createUser,
		"POST /utilisateurs/mot-de-passe":                     s.changePassword,
		"POST /utilisateurs/{id}/supprimer":                   s.deleteUser,
		"GET /plus":                                           s.morePage,
	} {
		var handler http.Handler = h
		if strings.HasPrefix(pattern, "POST ") {
			handler = s.afterChange(handler)
		}
		mux.Handle(pattern, s.requireUser(handler.ServeHTTP))
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

	return securityHeaders(logRequests(csrf.Handler(mux)), s.secureCookies), nil
}

// render affiche une page dans la mise en page commune, avec le message
// de confirmation éventuellement laissé par l'action précédente et le
// nombre d'alertes à lire (cloche de l'en-tête).
func (s *Server) render(w http.ResponseWriter, r *http.Request, status int, page, title string, data any) {
	v := view{Title: title, Path: r.URL.Path, Theme: auth.ThemeAuto, User: currentUser(r.Context()), Data: data}
	if v.User != nil {
		v.Theme = v.User.Theme
		v.Flash = s.popFlash(w, r)
		var err error
		if v.Alerts, err = s.alerts.OpenCount(r.Context()); err != nil {
			slog.WarnContext(r.Context(), "décompte des alertes", "err", err)
		}
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
	if errors.Is(err, ledger.ErrNotFound) || errors.Is(err, categorize.ErrNotFound) || errors.Is(err, importer.ErrNotFound) ||
		errors.Is(err, recurring.ErrNotFound) || errors.Is(err, attachments.ErrNotFound) || errors.Is(err, loans.ErrNotFound) ||
		errors.Is(err, goals.ErrNotFound) || errors.Is(err, scenarios.ErrNotFound) || errors.Is(err, household.ErrNotFound) ||
		errors.Is(err, backup.ErrNotFound) || errors.Is(err, auth.ErrNotFound) {
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

// longTransfer est le délai accordé à un transfert volumineux : fichier
// déposé (relevé, pièce jointe, sauvegarde) ou produit (export, sauvegarde).
// Les délais ordinaires du serveur (lecture 30 s, écriture 60 s) protègent
// des clients lents mais couperaient ces requêtes sur une connexion modeste.
const longTransfer = 30 * time.Minute

// allowLongTransfer prolonge les délais de lecture et d'écriture de la
// requête en cours.
func allowLongTransfer(w http.ResponseWriter) {
	rc := http.NewResponseController(w)
	deadline := time.Now().Add(longTransfer)
	// Sans prise en charge (serveur de test), les délais restent ceux du
	// serveur : rien à faire.
	_ = rc.SetReadDeadline(deadline)
	_ = rc.SetWriteDeadline(deadline)
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
