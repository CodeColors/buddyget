package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/codecolors/buddyget/internal/platform/calendar"
)

func TestForecastScreen(t *testing.T) {
	e := loggedIn(t)
	e.createAccount("Courant", "1000")
	account := idOf(t, e.do("GET", "/comptes", nil).body, "comptes", "Courant")
	today := calendar.Today()
	start := today.AddDays(10)
	e.expectRedirect("POST", "/recurrences", url.Values{
		"type": {"depense"}, "amount": {"1200"}, "label": {"Loyer"}, "period": {"1-mois"}, "business_day": {"aucun"},
		"start": {string(start)}, "account": {account},
	}, "/recurrences")
	rule := idOf(t, e.do("GET", "/recurrences", nil).body, "recurrences", "Loyer")

	// Sans paramètre : tous les comptes courants, à trois mois.
	target := calendar.FromTime(today.Time().AddDate(0, 3, 0))
	page := e.do("GET", "/previsionnel", nil)
	for _, want := range []string{
		`href="/previsionnel" aria-current="page"`, "Solde prévu le " + target.Format(), `class="chart-svg"`, "Point bas",
		"Le solde passe sous le seuil", start.MonthLabel(), "Loyer", `name="date" value="` + string(start) + `"`,
		`aria-current="true">3 mois</a>`,
	} {
		if !strings.Contains(page.body, want) {
			t.Errorf("prévisionnel : %q absent", want)
		}
	}
	if page.status != http.StatusOK {
		t.Fatalf("prévisionnel : %d", page.status)
	}

	// Écarter ponctuellement la première échéance, puis la réintégrer.
	self := "/previsionnel?date=" + string(target)
	exclude := url.Values{"rule": {rule}, "date": {string(start)}, "exclure": {"1"}, "back": {self}}
	e.expectRedirect("POST", "/previsionnel/exclusions", exclude, self+"#echeances")
	body := e.expectFlash(self, "Échéance écartée du prévisionnel.")
	if strings.Count(body, "is-excluded") != 1 || !strings.Contains(body, `name="exclure" value="0"`) {
		t.Error("l'échéance écartée doit rester listée, barrée")
	}
	exclude.Set("exclure", "0")
	exclude.Set("back", "https://ailleurs.example/")
	e.expectRedirect("POST", "/previsionnel/exclusions", exclude, "/previsionnel#echeances")
	if body := e.expectFlash(self, "Échéance réintégrée au prévisionnel."); strings.Contains(body, "is-excluded") {
		t.Error("l'échéance doit être réintégrée")
	}

	// Réglages : seuil négatif (découvert autorisé), solde pointé, six mois.
	settings := url.Values{"threshold": {"-500"}, "cleared": {"1"}, "average": {"6"}, "back": {self}}
	e.expectRedirect("POST", "/previsionnel/reglages", settings, self)
	body = e.expectFlash(self, "Réglages du prévisionnel enregistrés.")
	for _, want := range []string{`value="-500,00"`, `name="cleared" value="1" checked`, `value="6" selected`, "Solde pointé"} {
		if !strings.Contains(body, want) {
			t.Errorf("réglages : %q absent", want)
		}
	}
	settings.Set("average", "5")
	e.expectRedirect("POST", "/previsionnel/reglages", settings, self)
	e.expectFlash(self, "Choisissez la période")

	for query, want := range map[string]string{
		"?date=2020-01-01":                     "Choisissez une date à venir.",
		"?date=demain":                         "Date invalide.",
		"?date=" + string(today.AddDays(1000)): "24 mois au plus",
	} {
		if res := e.do("GET", "/previsionnel"+query, nil); res.status != http.StatusUnprocessableEntity || !strings.Contains(res.body, want) {
			t.Errorf("%s : %d, %q attendu", query, res.status, want)
		}
	}
	if res := e.do("GET", "/previsionnel?compte=999&date="+string(target), nil); res.status != http.StatusNotFound {
		t.Errorf("compte inconnu : %d", res.status)
	}
}
