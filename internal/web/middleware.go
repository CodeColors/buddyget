package web

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/codecolors/buddyget/internal/auth"
)

// contentSecurityPolicy n'autorise que des ressources servies par
// l'application elle-même : aucun CDN, aucun script ni style en ligne.
const contentSecurityPolicy = "default-src 'self'; img-src 'self' data:; object-src 'none'; " +
	"base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// permissionsPolicy refuse les fonctions du navigateur inutiles ; le micro
// reste permis au site pour la dictée de la saisie rapide.
const permissionsPolicy = "camera=(), geolocation=(), payment=(), usb=(), microphone=(self)"

// hstsPolicy impose HTTPS au navigateur une fois BuddyGet publié en HTTPS
// (BASE_URL), sans engager les sous-domaines.
const hstsPolicy = "max-age=15552000"

// securityHeaders pose les en-têtes de sécurité ; https ajoute HSTS.
func securityHeaders(next http.Handler, https bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("Permissions-Policy", permissionsPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		if https {
			h.Set("Strict-Transport-Security", hstsPolicy)
		}
		next.ServeHTTP(w, r)
	})
}

// statusRecorder retient le code de réponse pour la journalisation.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// logRequests journalise chaque requête en niveau debug. Seul le chemin est
// consigné, jamais la chaîne de requête ni le corps, qui peuvent contenir
// des données bancaires.
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !slog.Default().Enabled(r.Context(), slog.LevelDebug) {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		slog.LogAttrs(r.Context(), slog.LevelDebug, "requête",
			slog.String("méthode", r.Method),
			slog.String("chemin", r.URL.Path),
			slog.Int("statut", rec.status),
			slog.Duration("durée", time.Since(start)))
	})
}

type userKey struct{}

// currentUser renvoie l'utilisateur connecté, ou nil.
func currentUser(ctx context.Context) *auth.User {
	u, _ := ctx.Value(userKey{}).(*auth.User)
	return u
}

// requireUser réserve une page aux utilisateurs connectés et prolonge leur
// session au besoin.
func (s *Server) requireUser(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess, err := s.session(r)
		if errors.Is(err, auth.ErrNoSession) {
			s.clearSessionCookie(w)
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		if sess.Renewed {
			s.setSessionCookie(w, sess)
		}
		next(w, r.WithContext(context.WithValue(r.Context(), userKey{}, &sess.User)))
	})
}

// afterChange demande un examen des alertes après chaque modification
// réussie (redirection 303) : saisie, import, budget, récurrence… L'examen a
// lieu en arrière-plan, les demandes rapprochées n'en font qu'une.
func (s *Server) afterChange(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if rec.status == http.StatusSeeOther {
			s.alerts.Trigger()
		}
	})
}

// clientIP renvoie l'adresse du client. X-Forwarded-For n'est lu que si la
// connexion vient d'un proxy de confiance ; on remonte alors la chaîne
// depuis la droite jusqu'à la première adresse non fiable.
func clientIP(r *http.Request, trusted []netip.Prefix) netip.Addr {
	ap, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return netip.Addr{}
	}
	ip := ap.Addr().Unmap()
	if !isTrusted(ip, trusted) {
		return ip
	}
	hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		hop, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			break
		}
		ip = hop.Unmap()
		if !isTrusted(ip, trusted) {
			break
		}
	}
	return ip
}

func isTrusted(ip netip.Addr, trusted []netip.Prefix) bool {
	for _, p := range trusted {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}
