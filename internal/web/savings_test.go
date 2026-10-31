package web

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/codecolors/buddyget/internal/platform/calendar"
)

func (e *testEnv) savingsAccount(form url.Values) string {
	e.t.Helper()
	e.expectRedirect("POST", "/comptes", form, "/comptes")
	return idOf(e.t, e.do("GET", "/comptes", nil).body, "comptes", form.Get("name"))
}

func TestSavingsScreens(t *testing.T) {
	e := loggedIn(t)
	today := calendar.Today()
	opened := calendar.FromTime(today.Time().AddDate(-1, -2, 0))
	lastYear := strconv.Itoa(today.Time().Year() - 1)

	form := e.do("GET", "/comptes/nouveau?modele=livret_a", nil).body
	for _, want := range []string{`value="Livret A"`, `value="1,70"`, `value="22950,00"`, "relevées le 31/10/2026", `value="quinzaine" selected`,
		`aria-current="true">Livret A</a>`, `value="epargne" selected`} {
		if !strings.Contains(form, want) {
			t.Errorf("modèle Livret A : %q absent", want)
		}
	}
	if n := strings.Count(form, `aria-current="true"`); n != 1 {
		t.Errorf("seul le modèle choisi est mis en avant : %d", n)
	}
	livretForm := url.Values{
		"name": {"Livret A"}, "kind": {"epargne"}, "bank": {"autre"}, "opening_balance": {"1000"}, "opening_date": {string(opened)},
		"product": {"livret_a"}, "rate": {"abc"}, "method": {"quinzaine"}, "ceiling": {"22950"}, "lock": {"aucun"}, "auto_interest": {"1"},
	}
	if res := e.do("POST", "/comptes", livretForm); res.status != http.StatusUnprocessableEntity || !strings.Contains(res.body, "Taux invalide") {
		t.Errorf("taux invalide : %d", res.status)
	}
	livretForm.Set("rate", "1,70")
	livret := e.savingsAccount(livretForm)
	if body := e.do("GET", "/comptes/"+livret, nil).body; !strings.Contains(body, `value="1,70"`) || !strings.Contains(body, `value="22950,00"`) {
		t.Error("les réglages d'épargne doivent être repris à la modification")
	}

	page := e.do("GET", "/epargne", nil).body
	for _, want := range []string{"Total épargné", "1,70 %", "Par quinzaine", "Encore 21 950,00 € avant le plafond",
		`id="evolution-titre"`, "Inscrire les intérêts " + lastYear, "Total le "} {
		if !strings.Contains(page, want) {
			t.Errorf("vue épargne : %q absent", want)
		}
	}
	e.expectRedirect("POST", "/epargne/"+livret+"/interets", url.Values{"annee": {lastYear}}, "/epargne")
	if body := e.expectFlash("/epargne", "Intérêts "+lastYear+" inscrits"); strings.Contains(body, "Inscrire les intérêts "+lastYear) {
		t.Error("les intérêts inscrits ne sont plus proposés")
	}
	if body := e.do("GET", "/operations?compte="+livret, nil).body; !strings.Contains(body, `<span class="badge">Intérêts</span>`) {
		t.Error("l'écriture d'intérêts doit être signalée dans la liste des opérations")
	}

	// Retrait sur un PEL bloqué : avertissement, pénalité, confirmation.
	pel := e.savingsAccount(url.Values{
		"name": {"PEL"}, "kind": {"epargne"}, "bank": {"autre"}, "opening_balance": {"5000"}, "opening_date": {string(opened)},
		"product": {"pel"}, "rate": {"2"}, "method": {"compose_annuel"}, "lock": {"date"}, "unlock_date": {string(today.AddDays(365))}, "penalty": {"1"},
	})
	withdrawal := url.Values{"type": {"depense"}, "amount": {"100"}, "label": {"Retrait"}, "date": {string(today)}, "account": {pel}, "back": {"/operations"}}
	res := e.do("POST", "/operations", withdrawal)
	if res.status != http.StatusConflict || !strings.Contains(res.body, "Retrait anticipé") || !strings.Contains(res.body, "Pénalité estimée : 1,00 €") ||
		!strings.Contains(res.body, `name="confirm_unlock"`) {
		t.Errorf("retrait anticipé : %d", res.status)
	}
	withdrawal.Set("confirm_unlock", "1")
	e.expectRedirect("POST", "/operations", withdrawal, "/operations")

	// Versement programmé depuis le compte courant.
	e.createAccount("Courant", "0")
	courant := idOf(t, e.do("GET", "/comptes", nil).body, "comptes", "Courant")
	if body := e.do("GET", "/recurrences/nouvelle?vers="+livret, nil).body; !strings.Contains(body, `value="virement" checked`) ||
		!strings.Contains(body, `<option value="`+livret+`" selected>Livret A</option>`) {
		t.Error("« Programmer un versement » préremplit un virement vers le compte")
	}
	e.expectRedirect("POST", "/recurrences", url.Values{
		"type": {"virement"}, "amount": {"100"}, "label": {"Épargne mensuelle"}, "period": {"1-mois"}, "business_day": {"suivant"},
		"start": {string(today.AddDays(5))}, "account": {courant}, "to_account": {livret},
	}, "/recurrences")
	if body := e.do("GET", "/recurrences", nil).body; !strings.Contains(body, "Courant → Livret A") {
		t.Error("la liste des récurrences montre le compte destinataire")
	}
	if body := e.do("GET", "/epargne", nil).body; !strings.Contains(body, "Épargne mensuelle</a> · Chaque mois : 100,00 € depuis Courant") {
		t.Error("la vue épargne liste les versements programmés")
	}
	if body := e.do("GET", "/previsionnel?compte=epargne&date="+string(today.AddDays(400)), nil).body; !strings.Contains(body, `value="epargne" selected`) ||
		!strings.Contains(body, "Intérêts estimés") || !strings.Contains(body, "Épargne mensuelle") {
		t.Error("le prévisionnel de l'épargne comprend versements et intérêts estimés")
	}
}
