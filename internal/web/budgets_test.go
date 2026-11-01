package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/codecolors/buddyget/internal/platform/calendar"
)

func TestBudgetsAndDashboard(t *testing.T) {
	e := loggedIn(t)
	e.createAccount("Courant", "1000")
	account := idOf(t, e.do("GET", "/comptes", nil).body, "comptes", "Courant")
	if body := e.do("GET", "/budgets", nil).body; !strings.Contains(body, "Aucune enveloppe") {
		t.Error("budgets vides")
	}
	form := e.do("GET", "/budgets/nouveau", nil).body
	loisirs := regexpFind(t, form, `<option value="(\d+)">Loisirs</option>`)
	if strings.Contains(form, ">Salaires</option>") {
		t.Error("seules les catégories de dépenses ont une enveloppe")
	}
	today := calendar.Today()
	e.expectRedirect("POST", "/operations", url.Values{
		"type": {"depense"}, "amount": {"90"}, "label": {"Concert"}, "date": {string(today)}, "account": {account}, "category": {loisirs}, "back": {"/operations"},
	}, "/operations")

	if res := e.do("POST", "/budgets", url.Values{"category": {loisirs}, "amount": {"zéro"}}); res.status != http.StatusUnprocessableEntity {
		t.Errorf("plafond invalide : %d", res.status)
	}
	e.expectRedirect("POST", "/budgets", url.Values{"category": {loisirs}, "amount": {"100"}}, "/budgets")
	page := e.expectFlash("/budgets", "Enveloppe enregistrée.")
	for _, want := range []string{"Loisirs", "Bientôt atteint", "90,00 € sur 100,00 €", "reste 10,00 €", `id="tendance-titre"`, today.MonthLabel()} {
		if !strings.Contains(page, want) {
			t.Errorf("budgets : %q absent", want)
		}
	}
	dash := e.do("GET", "/", nil).body
	for _, want := range []string{"Reste à vivre en " + today.MonthLabel(), "Dépenses déjà faites", "Enveloppes du mois", "Bientôt atteint"} {
		if !strings.Contains(dash, want) {
			t.Errorf("tableau de bord : %q absent", want)
		}
	}
	if body := e.do("GET", "/previsionnel", nil).body; !strings.Contains(body, `<span class="badge">Enveloppe</span>`) {
		t.Error("le prévisionnel utilise l'enveloppe")
	}

	if body := e.do("GET", "/budgets/"+loisirs, nil).body; !strings.Contains(body, `value="100,00"`) || !strings.Contains(body, "Supprimer l'enveloppe") {
		t.Error("modification de l'enveloppe")
	}
	e.expectRedirect("POST", "/budgets", url.Values{"existing": {"1"}, "category": {loisirs}, "amount": {"50"}, "carry_over": {"1"}}, "/budgets")
	if body := e.do("GET", "/budgets", nil).body; !strings.Contains(body, "Dépassé") || !strings.Contains(body, "dépassement de 40,00 €") {
		t.Error("enveloppe dépassée")
	}
	prev := calendar.FromTime(today.Time().AddDate(0, -1, 0))
	if body := e.do("GET", "/budgets?mois="+string(prev)[:7], nil).body; !strings.Contains(body, prev.MonthLabel()) {
		t.Error("navigation entre les mois")
	}

	// Alertes : l'enveloppe dépassée est signalée, la cloche le montre.
	if _, err := e.alerts.Evaluate(t.Context(), today); err != nil {
		t.Fatal(err)
	}
	if body := e.do("GET", "/", nil).body; !strings.Contains(body, `class="bell-count"`) || !strings.Contains(body, "Enveloppe « Loisirs » dépassée") {
		t.Error("cloche et alertes du tableau de bord")
	}
	alertsPage := e.do("GET", "/alertes", nil).body
	id := regexpFind(t, alertsPage, `name="id" value="([1-9]\d*)"`)
	e.expectRedirect("POST", "/alertes/acquitter", url.Values{"id": {id}}, "/alertes")
	if body := e.expectFlash("/alertes", "Alerte acquittée."); !strings.Contains(body, "Déjà lues") {
		t.Error("alerte acquittée rangée à part")
	}
	e.expectRedirect("POST", "/alertes/acquitter", url.Values{"id": {"0"}}, "/alertes")
	if body := e.expectFlash("/alertes", "Toutes les alertes sont acquittées."); strings.Contains(body, `class="bell-count"`) {
		t.Error("plus rien à lire")
	}

	e.expectRedirect("POST", "/budgets/"+loisirs+"/supprimer", nil, "/budgets")
	if body := e.expectFlash("/budgets", "Enveloppe supprimée."); !strings.Contains(body, "Aucune enveloppe") {
		t.Error("enveloppe supprimée")
	}
}

func TestAlertSettings(t *testing.T) {
	e := loggedIn(t)
	page := e.do("GET", "/alertes/reglages", nil).body
	for _, want := range []string{"Dépense inhabituelle", `name="param_budget" type="number" value="80"`, "désactivées par défaut", `value="immediate" selected`} {
		if !strings.Contains(page, want) {
			t.Errorf("réglages : %q absent", want)
		}
	}
	settings := url.Values{}
	for _, k := range []string{"budget", "prevision", "inhabituelle", "abonnement", "echeance", "import", "deblocage"} {
		settings.Set("enabled_"+k, "1")
		settings.Set("param_"+k, "5")
	}
	settings.Del("enabled_echeance")
	settings.Set("param_inhabituelle", "1")
	if res := e.do("POST", "/alertes/reglages", settings); res.status != http.StatusUnprocessableEntity || !strings.Contains(res.body, "Dépense inhabituelle : valeur entre 2 et 20") {
		t.Errorf("réglage invalide : %d", res.status)
	}
	settings.Set("param_inhabituelle", "4")
	e.expectRedirect("POST", "/alertes/reglages", settings, "/alertes/reglages")
	if body := e.expectFlash("/alertes/reglages", "Réglages des alertes enregistrés."); !strings.Contains(body, `name="param_inhabituelle" type="number" value="4"`) ||
		strings.Contains(body, `name="enabled_echeance" value="1" checked`) {
		t.Error("réglages repris")
	}

	e.expectRedirect("POST", "/alertes/essai", nil, "/alertes/reglages")
	e.expectFlash("/alertes/reglages", "Activez et enregistrez d&#39;abord un canal.")

	var received string
	hook := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { received = r.Header.Get("Title") }))
	defer hook.Close()
	channels := url.Values{"webhook_enabled": {"1"}, "webhook_url": {"ftp://x"}, "frequency": {"daily"}, "smtp_security": {"starttls"}, "smtp_port": {"587"},
		"smtp_password": {"secret-smtp"}, "discreet": {"1"}}
	if res := e.do("POST", "/alertes/canaux", channels); res.status != http.StatusUnprocessableEntity || !strings.Contains(res.body, "Adresse du webhook invalide") {
		t.Errorf("webhook invalide : %d", res.status)
	}
	channels.Set("webhook_url", hook.URL)
	e.expectRedirect("POST", "/alertes/canaux", channels, "/alertes/reglages")
	if body := e.expectFlash("/alertes/reglages", "Canaux de notification enregistrés."); strings.Contains(body, "secret-smtp") || !strings.Contains(body, `value="daily" selected`) {
		t.Error("le mot de passe SMTP n'est jamais réaffiché")
	}
	e.expectRedirect("POST", "/alertes/essai", nil, "/alertes/reglages")
	if e.expectFlash("/alertes/reglages", "Notification d&#39;essai envoyée."); received == "" {
		t.Error("le webhook doit recevoir l'essai")
	}
}

// regexpFind renvoie le premier groupe capturé par pattern dans body.
func regexpFind(t *testing.T, body, pattern string) string {
	t.Helper()
	m := regexp.MustCompile(pattern).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("motif %q introuvable", pattern)
	}
	return m[1]
}
