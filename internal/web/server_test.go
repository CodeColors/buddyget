package web

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/codecolors/buddyget/internal/auth"
	"github.com/codecolors/buddyget/internal/budget"
	"github.com/codecolors/buddyget/internal/categorize"
	"github.com/codecolors/buddyget/internal/config"
	"github.com/codecolors/buddyget/internal/forecast"
	"github.com/codecolors/buddyget/internal/importer"
	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/platform/db"
	"github.com/codecolors/buddyget/internal/recurring"
	"github.com/codecolors/buddyget/internal/savings"
)

const password = "un mot de passe solide"

type testEnv struct {
	t      *testing.T
	srv    *httptest.Server
	client *http.Client
	auth   *auth.Service
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	d, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "web.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	svc := auth.NewService(d)
	dataDir := t.TempDir()
	accounts, rules, recurrences := ledger.NewService(d), categorize.NewService(d), recurring.NewService(d)
	savingsAccounts, budgets := savings.NewService(d, accounts), budget.NewService(d, accounts)
	h, err := New(config.Config{MaxUploadMB: 1}, d, Services{
		Auth: svc, Ledger: accounts, Rules: rules,
		Importer: importer.NewService(d, dataDir, 1<<20, rules), Recurring: recurrences,
		Savings: savingsAccounts, Forecast: forecast.NewService(d, accounts, recurrences, savingsAccounts, budgets),
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return &testEnv{t: t, srv: srv, client: client, auth: svc}
}

// result est une réponse déjà lue et fermée.
type result struct {
	status  int
	header  http.Header
	cookies []*http.Cookie
	body    string
}

// do envoie une requête et renvoie sa réponse.
func (e *testEnv) do(method, path string, form url.Values, headers ...string) result {
	e.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(e.t.Context(), method, e.srv.URL+path, body)
	if err != nil {
		e.t.Fatal(err)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := e.client.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		e.t.Fatal(err)
	}
	return result{status: resp.StatusCode, header: resp.Header, cookies: resp.Cookies(), body: string(b)}
}

func (e *testEnv) expectRedirect(method, path string, form url.Values, to string) result {
	e.t.Helper()
	res := e.do(method, path, form)
	if res.status != http.StatusSeeOther || res.header.Get("Location") != to {
		e.t.Fatalf("%s %s : %d → %q, attendu 303 → %q", method, path, res.status, res.header.Get("Location"), to)
	}
	return res
}

func (e *testEnv) createUser() {
	e.t.Helper()
	if _, err := e.auth.CreateFirstUser(e.t.Context(), "alice", password); err != nil {
		e.t.Fatal(err)
	}
}

func (e *testEnv) login() {
	e.t.Helper()
	e.expectRedirect("POST", "/login", url.Values{"username": {"alice"}, "password": {password}}, "/")
}

func TestFirstRunSetup(t *testing.T) {
	e := newTestEnv(t)
	e.expectRedirect("GET", "/", nil, "/login")
	e.expectRedirect("GET", "/login", nil, "/setup")

	res := e.do("GET", "/setup", nil)
	if res.status != http.StatusOK || !strings.Contains(res.body, `action="/setup"`) {
		t.Fatalf("page de création : %d", res.status)
	}

	res = e.do("POST", "/setup", url.Values{"username": {"alice"}, "password": {password}, "confirm": {"autre chose encore"}})
	if res.status != http.StatusUnprocessableEntity || !strings.Contains(res.body, "ne correspondent pas") || !strings.Contains(res.body, `value="alice"`) {
		t.Errorf("confirmation différente : %d", res.status)
	}
	res = e.do("POST", "/setup", url.Values{"username": {"alice"}, "password": {"court"}, "confirm": {"court"}})
	if res.status != http.StatusUnprocessableEntity || !strings.Contains(res.body, "au moins 12 caractères") {
		t.Errorf("mot de passe trop court : %d", res.status)
	}

	res = e.expectRedirect("POST", "/setup", url.Values{"username": {"alice"}, "password": {password}, "confirm": {password}}, "/")
	c := sessionCookieOf(t, res)
	if !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Secure {
		t.Errorf("attributs du cookie inattendus : %+v", c)
	}

	res = e.do("GET", "/", nil)
	if !strings.Contains(res.body, "Bonjour alice") {
		t.Error("le tableau de bord doit accueillir l'utilisateur créé")
	}
	e.expectRedirect("GET", "/setup", nil, "/login")
	e.expectRedirect("POST", "/setup", url.Values{"username": {"bob"}, "password": {password}, "confirm": {password}}, "/login")
}

func TestLoginLogout(t *testing.T) {
	e := newTestEnv(t)
	e.createUser()

	res := e.do("POST", "/login", url.Values{"username": {"alice"}, "password": {"mauvais mot de passe"}})
	if res.status != http.StatusUnauthorized || !strings.Contains(res.body, "Identifiant ou mot de passe incorrect.") {
		t.Fatalf("mauvais mot de passe : %d", res.status)
	}
	if strings.Contains(res.body, "mauvais mot de passe") {
		t.Error("le mot de passe saisi ne doit jamais être renvoyé")
	}

	e.login()
	e.expectRedirect("GET", "/login", nil, "/")
	if res := e.do("GET", "/", nil); res.status != http.StatusOK {
		t.Fatalf("tableau de bord : %d", res.status)
	}

	res = e.expectRedirect("POST", "/logout", nil, "/login")
	if c := sessionCookieOf(t, res); c.MaxAge >= 0 {
		t.Error("la déconnexion doit effacer le cookie")
	}
	e.expectRedirect("GET", "/", nil, "/login")
}

func TestLoginThrottled(t *testing.T) {
	e := newTestEnv(t)
	e.createUser()
	bad := url.Values{"username": {"alice"}, "password": {"mauvais mot de passe"}}
	for range 5 {
		e.do("POST", "/login", bad)
	}
	res := e.do("POST", "/login", url.Values{"username": {"alice"}, "password": {password}})
	if res.status != http.StatusTooManyRequests || res.header.Get("Retry-After") != "900" || !strings.Contains(res.body, "Réessayez dans 15 min.") {
		t.Errorf("blocage attendu : %d, Retry-After %q", res.status, res.header.Get("Retry-After"))
	}
}

func TestStaleSessionCookie(t *testing.T) {
	e := newTestEnv(t)
	e.createUser()
	u, _ := url.Parse(e.srv.URL)
	e.client.Jar.SetCookies(u, []*http.Cookie{{Name: sessionCookie, Value: "jeton-inconnu"}})
	res := e.expectRedirect("GET", "/", nil, "/login")
	if c := sessionCookieOf(t, res); c.MaxAge >= 0 {
		t.Error("un cookie de session invalide doit être effacé")
	}
}

func TestThemePreference(t *testing.T) {
	e := newTestEnv(t)
	e.createUser()

	if res := e.do("GET", "/login", nil); !strings.Contains(res.body, `data-theme="auto"`) {
		t.Error("une page anonyme suit le thème de l'appareil")
	}
	e.login()
	e.expectRedirect("POST", "/preferences/theme", url.Values{"theme": {"dark"}, "back": {"/"}}, "/")
	res := e.do("GET", "/", nil)
	if !strings.Contains(res.body, `data-theme="dark"`) || !strings.Contains(res.body, `value="dark" aria-pressed="true"`) {
		t.Error("le thème sombre doit être appliqué dès le rendu serveur")
	}

	if res := e.do("POST", "/preferences/theme", url.Values{"theme": {"rose"}}); res.status != http.StatusBadRequest {
		t.Errorf("thème inconnu : %d, attendu 400", res.status)
	}
	e.expectRedirect("POST", "/preferences/theme", url.Values{"theme": {"light"}, "back": {"//exemple.com"}}, "/")
}

func TestCrossOriginRequestsRejected(t *testing.T) {
	e := newTestEnv(t)
	e.createUser()
	form := url.Values{"username": {"alice"}, "password": {password}}
	for _, h := range [][]string{
		{"Sec-Fetch-Site", "cross-site"},
		{"Origin", "https://site-malveillant.example"},
	} {
		res := e.do("POST", "/login", form, h...)
		if res.status != http.StatusForbidden || !strings.Contains(res.body, "Accès refusé") {
			t.Errorf("%s : %d, attendu 403", h[0], res.status)
		}
	}
	if res := e.do("POST", "/login", form, "Sec-Fetch-Site", "same-origin"); res.status != http.StatusSeeOther {
		t.Errorf("requête de même origine refusée : %d", res.status)
	}
}

func TestHealthz(t *testing.T) {
	e := newTestEnv(t)
	res := e.do("GET", "/healthz", nil)
	if res.status != http.StatusOK || res.body != "ok\n" || res.header.Get("Cache-Control") != "no-store" {
		t.Errorf("healthz : %d %q", res.status, res.body)
	}
}

func TestStaticAssets(t *testing.T) {
	e := newTestEnv(t)
	e.createUser()
	res := e.do("GET", "/login", nil)
	cssURL := regexp.MustCompile(`href="(/static/app\.css\?v=[0-9a-f]{12})"`).FindStringSubmatch(res.body)
	if cssURL == nil {
		t.Fatal("la page doit référencer la feuille de style versionnée")
	}

	css := e.do("GET", cssURL[1], nil)
	if css.status != http.StatusOK || css.header.Get("Cache-Control") != "public, max-age=31536000, immutable" ||
		!strings.HasPrefix(css.header.Get("Content-Type"), "text/css") {
		t.Errorf("CSS versionnée : %d %v", css.status, css.header)
	}
	if res := e.do("GET", "/static/app.css?v=ancienne", nil); res.header.Get("Cache-Control") != "no-cache" {
		t.Error("une version périmée ne doit pas être mise en cache durablement")
	}
	if res := e.do("GET", "/static/app.css", nil, "If-None-Match", css.header.Get("ETag")); res.status != http.StatusNotModified {
		t.Errorf("revalidation : %d, attendu 304", res.status)
	}
	if res := e.do("GET", "/static/manifest.webmanifest", nil); res.header.Get("Content-Type") != "application/manifest+json" {
		t.Errorf("type du manifeste : %q", res.header.Get("Content-Type"))
	}
	if res := e.do("GET", "/static/absent.js", nil); res.status != http.StatusNotFound {
		t.Errorf("fichier absent : %d", res.status)
	}
}

func TestSecurityHeadersAndNotFound(t *testing.T) {
	e := newTestEnv(t)
	res := e.do("GET", "/nexiste-pas", nil)
	if res.status != http.StatusNotFound || !strings.Contains(res.body, "Page introuvable") {
		t.Errorf("404 : %d", res.status)
	}
	for h, want := range map[string]string{
		"Content-Security-Policy": contentSecurityPolicy,
		"X-Content-Type-Options":  "nosniff",
		"Referrer-Policy":         "same-origin",
	} {
		if got := res.header.Get(h); got != want {
			t.Errorf("%s = %q, attendu %q", h, got, want)
		}
	}
}

func TestClientIP(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("172.16.0.0/12")}
	cases := []struct {
		name, remote, xff, want string
	}{
		{"direct", "192.0.2.1:1234", "", "192.0.2.1"},
		{"en-tête ignoré hors proxy de confiance", "192.0.2.1:1234", "203.0.113.9", "192.0.2.1"},
		{"derrière le proxy", "172.17.0.1:1234", "203.0.113.9", "203.0.113.9"},
		{"chaîne de proxys", "172.17.0.1:1234", "198.51.100.2, 203.0.113.9, 172.18.0.5", "203.0.113.9"},
		{"proxy sans en-tête", "172.17.0.1:1234", "", "172.17.0.1"},
		{"IPv4 dans IPv6", "[::ffff:192.0.2.1]:1234", "", "192.0.2.1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(t.Context(), "GET", "/", nil)
			r.RemoteAddr = c.remote
			if c.xff != "" {
				r.Header.Set("X-Forwarded-For", c.xff)
			}
			if got := clientIP(r, trusted).String(); got != c.want {
				t.Errorf("clientIP = %s, attendu %s", got, c.want)
			}
		})
	}
}

func TestLocalPath(t *testing.T) {
	for in, want := range map[string]string{
		"/":                "/",
		"/operations?p=2":  "/operations?p=2",
		"":                 "/",
		"https://ex.com/":  "/",
		"//ex.com":         "/",
		`/\ex.com`:         "/",
		"javascript:alert": "/",
	} {
		if got := localPath(in); got != want {
			t.Errorf("localPath(%q) = %q, attendu %q", in, got, want)
		}
	}
}

func sessionCookieOf(t *testing.T, res result) *http.Cookie {
	t.Helper()
	for _, c := range res.cookies {
		if c.Name == sessionCookie {
			return c
		}
	}
	t.Fatal("cookie de session absent de la réponse")
	return nil
}
