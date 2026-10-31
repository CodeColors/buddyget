package web

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/codecolors/buddyget/internal/platform/calendar"
)

// monthlySeries saisit n dépenses mensuelles se terminant ce mois-ci.
func (e *testEnv) monthlySeries(account, label, amount string, n int) {
	e.t.Helper()
	today := calendar.Today()
	for i := range n {
		date := calendar.Monthly.Nth(today, i-n+1, false).AddDays(-2)
		e.expectRedirect("POST", "/operations", url.Values{
			"type": {"depense"}, "amount": {amount}, "label": {label}, "date": {string(date)}, "account": {account}, "back": {"/operations"},
		}, "/operations")
	}
}

var signatureField = regexp.MustCompile(`name="signature" value="([^"]+)"`)

func TestDetectedRecurrences(t *testing.T) {
	e := loggedIn(t)
	e.createAccount("Courant", "0")
	account := idOf(t, e.do("GET", "/comptes", nil).body, "comptes", "Courant")
	e.monthlySeries(account, "PRLV SEPA EDF", "78,32", 4)
	e.monthlySeries(account, "NETFLIX.COM", "17,99", 3)
	e.monthlySeries(account, "LOYER", "700", 3)

	if body := e.do("GET", "/recurrences", nil).body; !strings.Contains(body, "3 séries détectées") {
		t.Fatal("la liste doit signaler les séries détectées")
	}
	page := e.do("GET", "/recurrences/detectees", nil).body
	for _, want := range []string{"PRLV SEPA EDF", "Chaque mois", "<meter", "4 occurrences"} {
		if !strings.Contains(page, want) {
			t.Errorf("séries détectées : %q absent", want)
		}
	}
	sigs := signatureField.FindAllStringSubmatch(page, -1)
	if len(sigs) != 6 { // deux formulaires par série
		t.Fatalf("%d champs de signature, attendu 6", len(sigs))
	}
	// La plus sûre (quatre occurrences) est en tête.
	edf := sigs[0][1]

	e.expectRedirect("POST", "/recurrences/detectees/valider", url.Values{"signature": {edf}}, "/recurrences/detectees")
	e.expectFlash("/recurrences/detectees", "Récurrence validée.")
	list := e.do("GET", "/recurrences", nil).body
	if !strings.Contains(list, "PRLV SEPA EDF") || !strings.Contains(list, "Détectée") || !strings.Contains(list, "Prochaine échéance le") || !strings.Contains(list, "2 séries détectées") {
		t.Error("la récurrence validée doit apparaître, avec sa prochaine échéance")
	}

	// « Modifier » préremplit le formulaire ; l'origine reste « détectée ».
	page = e.do("GET", "/recurrences/detectees", nil).body
	var netflix, loyer string
	for _, m := range signatureField.FindAllStringSubmatch(page, -1) {
		switch {
		case strings.Contains(m[1], "netflix"):
			netflix = m[1]
		case strings.Contains(m[1], "loyer"):
			loyer = m[1]
		}
	}
	form := e.do("GET", "/recurrences/nouvelle?serie="+url.QueryEscape(netflix), nil).body
	for _, want := range []string{`value="17,99"`, `value="NETFLIX.COM"`, `name="origin" value="detectee"`, `name="serie" value="1|-|netflix com"`, `value="1-mois" selected`} {
		if !strings.Contains(form, want) {
			t.Errorf("formulaire prérempli : %q absent", want)
		}
	}
	start := regexp.MustCompile(`id="start" name="start" type="date" value="([0-9-]+)"`).FindStringSubmatch(form)[1]
	e.expectRedirect("POST", "/recurrences", url.Values{
		"origin": {"detectee"}, "serie": {netflix}, "type": {"depense"}, "amount": {"19,99"}, "label": {"Netflix"}, "period": {"1-mois"}, "business_day": {"suivant"},
		"start": {start}, "account": {account}, "category": {"0"},
	}, "/recurrences")
	e.expectRedirect("POST", "/recurrences/detectees/rejeter", url.Values{"signature": {loyer}}, "/recurrences/detectees")
	if body := e.expectFlash("/recurrences/detectees", "Série rejetée"); !strings.Contains(body, "Aucune nouvelle série détectée.") {
		t.Error("plus aucune série ne doit être proposée")
	}
	if list = e.do("GET", "/recurrences", nil).body; strings.Count(list, "Détectée</span>") != 2 || !strings.Contains(list, "−19,99") {
		t.Error("Netflix modifié doit être enregistré comme détecté, au nouveau montant")
	}
}

func TestManualRecurrence(t *testing.T) {
	e := loggedIn(t)
	e.createAccount("Courant", "0")
	account := idOf(t, e.do("GET", "/comptes", nil).body, "comptes", "Courant")

	form := url.Values{"type": {"revenu"}, "amount": {"150"}, "label": {"Pension"}, "period": {"autre"}, "business_day": {"precedent"},
		"custom_count": {"10"}, "custom_unit": {"jour"}, "start": {"2026-01-05"}, "end": {"2025-01-01"}, "account": {account}}
	if res := e.do("POST", "/recurrences", form); res.status != http.StatusUnprocessableEntity || !strings.Contains(res.body, "date de fin précède") {
		t.Errorf("fin avant le début : %d", res.status)
	}
	form.Set("end", "")
	e.expectRedirect("POST", "/recurrences", form, "/recurrences")
	list := e.expectFlash("/recurrences", "Récurrence enregistrée.")
	if !strings.Contains(list, "Tous les 10 jours") || !strings.Contains(list, "&#43;150,00") || strings.Contains(list, "Détectée") {
		t.Error("récurrence manuelle mal affichée")
	}

	id := idOf(t, list, "recurrences", "Pension")
	if body := e.do("GET", "/recurrences/"+id, nil).body; !strings.Contains(body, `value="autre" selected`) || !strings.Contains(body, `value="10"`) ||
		!strings.Contains(body, `value="precedent" selected`) {
		t.Error("la périodicité personnalisée et le report doivent être repris")
	}
	e.expectRedirect("POST", "/recurrences/"+id+"/activer", url.Values{"active": {"false"}}, "/recurrences")
	if body := e.expectFlash("/recurrences", "Récurrence suspendue."); !strings.Contains(body, "Suspendues ou terminées") {
		t.Error("la récurrence suspendue doit être rangée à part")
	}
	e.expectRedirect("POST", "/recurrences/"+id+"/supprimer", nil, "/recurrences")
	e.expectFlash("/recurrences", "Récurrence supprimée.")
	if res := e.do("GET", "/recurrences/"+id, nil); res.status != http.StatusNotFound {
		t.Errorf("récurrence supprimée : %d", res.status)
	}
}

func TestMoreMenu(t *testing.T) {
	e := loggedIn(t)
	body := e.do("GET", "/plus", nil).body
	for _, want := range []string{`class="nav-more" href="/plus" aria-current="page"`, `href="/regles"`, `href="/import/historique"`} {
		if !strings.Contains(body, want) {
			t.Errorf("page Plus : %q absent", want)
		}
	}
	for _, path := range []string{"/comptes", "/recurrences"} {
		if body := e.do("GET", path, nil).body; !strings.Contains(body, `class="nav-more" href="/plus" aria-current="page"`) {
			t.Errorf("« Plus » doit être actif sur l'écran secondaire %s", path)
		}
	}
}
