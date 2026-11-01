package web

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/platform/money"
)

// Écrans du jalon 10 : crédits, projets, lissage, scénarios.

// created suit la redirection d'une création vers sa fiche et renvoie son
// adresse.
func (e *testEnv) created(path string, form url.Values, prefix string) string {
	e.t.Helper()
	res := e.do("POST", path, form)
	to := res.header.Get("Location")
	if res.status != http.StatusSeeOther || !strings.HasPrefix(to, prefix) {
		e.t.Fatalf("POST %s : %d → %q, attendu 303 → %s…", path, res.status, to, prefix)
	}
	return strings.TrimSuffix(to, "#modifications")
}

func (e *testEnv) expectContains(path string, want ...string) string {
	e.t.Helper()
	body := e.do("GET", path, nil).body
	for _, w := range want {
		if !strings.Contains(body, w) {
			e.t.Errorf("%s : %q absent", path, w)
		}
	}
	return body
}

func TestLoanScreens(t *testing.T) {
	e := loggedIn(t)
	today := calendar.Today()
	e.expectContains("/credits", "Aucun crédit suivi")
	e.createAccount("Courant", "5000")
	account := idOf(t, e.do("GET", "/comptes", nil).body, "comptes", "Courant")

	form := url.Values{"name": {"Auto"}, "principal": {"12000"}, "rate": {"abc"}, "months": {"48"}, "insurance": {"8"},
		"first": {string(today.AddDays(5))}, "account": {account}}
	if res := e.do("POST", "/credits", form); res.status != http.StatusUnprocessableEntity || !strings.Contains(res.body, "Taux invalide") {
		t.Errorf("taux invalide : %d", res.status)
	}
	form.Set("rate", "4,9")
	loan := e.created("/credits", form, "/credits/")
	e.expectContains(loan, "Crédit Auto", "4,90 %", "Capital restant dû", "Coût des intérêts", "Tableau d'amortissement", "Restant dû", "Remboursement anticipé")
	e.expectContains(loan+"/modifier", `value="4,90"`, `value="12000,00"`, `value="48"`)
	e.expectContains("/recurrences", "Crédit Auto")

	e.expectRedirect("POST", loan+"/anticipe", url.Values{"date": {string(today.AddDays(40))}, "amount": {"2000"}}, loan)
	e.expectContains(loan, "Remboursement anticipé enregistré", "anticipé", "Remboursements anticipés")
	if res := e.do("POST", loan+"/anticipe", url.Values{"date": {string(today)}, "amount": {"0"}}); res.status != http.StatusUnprocessableEntity {
		t.Errorf("remboursement nul : %d", res.status)
	}
	e.expectContains("/credits", "par mois", "% du capital remboursé", "Auto")

	e.expectRedirect("POST", loan+"/supprimer", nil, "/credits")
	e.expectContains("/credits", "Crédit supprimé", "Aucun crédit suivi")
	if res := e.do("GET", loan, nil); res.status != http.StatusNotFound {
		t.Errorf("crédit supprimé : %d", res.status)
	}
}

func TestGoalScreens(t *testing.T) {
	e := loggedIn(t)
	today := calendar.Today()
	e.expectContains("/projets/nouveau", "créez-en un d'abord")
	e.createAccount("Courant", "3000")
	livret := e.savingsAccount(url.Values{
		"name": {"Livret A"}, "kind": {"epargne"}, "bank": {"autre"}, "opening_balance": {"2000"}, "opening_date": {"2026-01-01"},
		"product": {"livret_a"}, "rate": {"1,70"}, "method": {"quinzaine"}, "lock": {"aucun"},
	})
	target := calendar.FromTime(today.Time().AddDate(0, 8, 0))
	e.expectRedirect("POST", "/projets", url.Values{"name": {"Vacances"}, "target": {"1200"}, "target_date": {string(target)},
		"account": {livret}, "tag": {"vacances"}}, "/projets")
	body := e.expectContains("/projets", "Vacances", "Versement suggéré", money.Format(150_00)+" par mois", "aucun versement programmé", "En retard",
		"dont "+money.Format(2_000_00)+" non affectés", "Créer le versement")
	goal := "/projets/" + idOf(t, body, "projets", "Vacances")

	e.expectRedirect("POST", goal+"/cagnotte", url.Values{"amount": {"300"}, "sens": {"ajouter"}}, "/projets")
	e.expectContains("/projets", "Cagnotte mise à jour", money.Format(300_00)+"</strong> sur "+money.Format(1_200_00)+" (25 %)", "dont "+money.Format(1_700_00)+" non affectés")
	e.expectRedirect("POST", goal+"/cagnotte", url.Values{"amount": {"5000"}, "sens": {"ajouter"}}, "/projets")
	e.expectContains("/projets", "Il ne reste que "+money.Format(1_700_00))

	courant := idOf(t, e.do("GET", "/comptes", nil).body, "comptes", "Courant")
	e.expectRedirect("POST", goal+"/versement", url.Values{"from": {courant}}, "/projets")
	if body := e.expectContains("/projets", "Versement programmé créé", "atteint en", "En bonne voie"); strings.Contains(body, "Créer le versement") {
		t.Error("le versement programmé n'est plus proposé une fois créé")
	}
	e.expectContains("/recurrences", "Projet Vacances")

	e.expectContains(goal, `value="Vacances"`, `value="vacances"`)
	if res := e.do("POST", goal, url.Values{"name": {"Vacances"}, "target": {"1200"}, "target_date": {string(target)}, "account": {courant}}); res.status != http.StatusUnprocessableEntity {
		t.Errorf("compte courant refusé : %d", res.status)
	}
	e.expectRedirect("POST", goal+"/supprimer", nil, "/projets")
	e.expectContains("/projets", "Projet supprimé", "Aucun projet")
}

func TestSmoothingForm(t *testing.T) {
	e := loggedIn(t)
	e.createAccount("Courant", "3000")
	account := idOf(t, e.do("GET", "/comptes", nil).body, "comptes", "Courant")
	form := url.Values{"type": {"depense"}, "amount": {"600"}, "label": {"Assurance habitation"}, "period": {"1-mois"}, "business_day": {"suivant"},
		"start": {string(calendar.Today().AddDays(60))}, "account": {account}, "smooth": {"1"}, "smooth_account": {"0"}}
	if res := e.do("POST", "/recurrences", form); res.status != http.StatusUnprocessableEntity || !strings.Contains(res.body, "se lisse") {
		t.Errorf("lissage mensuel refusé : %d", res.status)
	}
	form.Set("period", "12-mois")
	e.expectRedirect("POST", "/recurrences", form, "/recurrences")
	body := e.expectContains("/recurrences", `<span class="badge">Lissée</span>`)
	rule := idOf(t, body, "recurrences", "Assurance habitation")
	e.expectContains("/recurrences/"+rule, `name="smooth" value="1" checked`)
	e.expectContains("/previsionnel", "Provision : Assurance habitation", "Provisions des dépenses lissées")
	e.expectContains("/", "Dépenses lissées")
}

func TestScenarioScreens(t *testing.T) {
	e := loggedIn(t)
	today := calendar.Today()
	e.createAccount("Courant", "3000")
	account := idOf(t, e.do("GET", "/comptes", nil).body, "comptes", "Courant")
	e.expectRedirect("POST", "/recurrences", url.Values{"type": {"revenu"}, "amount": {"2000"}, "label": {"Salaire"}, "period": {"1-mois"},
		"business_day": {"suivant"}, "start": {string(today.AddDays(10))}, "account": {account}}, "/recurrences")
	salary := idOf(t, e.do("GET", "/recurrences", nil).body, "recurrences", "Salaire")

	if res := e.do("POST", "/scenarios", url.Values{"name": {" "}}); res.status != http.StatusUnprocessableEntity {
		t.Errorf("nom vide : %d", res.status)
	}
	sc := e.created("/scenarios", url.Values{"name": {"Voiture"}}, "/scenarios/")
	e.expectContains(sc, "Aucune modification", "sans le scénario", "Voir le prévisionnel avec ce scénario")
	e.expectContains("/scenarios", "Voiture", "0 modification")

	items := sc + "/modifications"
	for _, form := range []url.Values{
		{"kind": {"ponctuel"}, "sens": {"depense"}, "amount": {"2000"}, "label": {"Apport"}, "account": {account}, "date": {string(today.AddDays(30))}},
		{"kind": {"credit"}, "label": {"Voiture"}, "account": {account}, "principal": {"15000"}, "rate": {"3"}, "duration": {"48"}, "date": {string(today.AddDays(60))}},
		{"kind": {"variation"}, "rule": {salary}, "change": {"hausse"}, "mode": {"pourcent"}, "value": {"10"}, "date": {string(today.AddDays(40))}},
	} {
		e.created(items, form, sc)
	}
	if res := e.do("POST", items, url.Values{"kind": {"variation"}, "rule": {salary}, "value": {""}, "date": {string(today)}}); res.status != http.StatusUnprocessableEntity ||
		!strings.Contains(res.body, "Indiquez un montant") {
		t.Errorf("variation vide : %d", res.status)
	}
	body := e.expectContains(sc, "Apport", "Crédit Voiture", "Nouveau crédit", "10,00\u00a0% à partir du", "Écart", "Convertir en données réelles")
	e.expectContains("/previsionnel?scenario="+strings.TrimPrefix(sc, "/scenarios/"), "avec le scénario « Voiture »", `<span class="badge">Scénario</span>`,
		"chart-reference", "Écart cumulé du scénario", `value="`+strings.TrimPrefix(sc, "/scenarios/")+`" selected`)

	// Retrait d'une modification, renommage, conversion.
	m := regexp.MustCompile(`Apport(?s:.)*?action="(` + sc + `/modifications/\d+/supprimer)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("bouton de retrait de l'apport introuvable")
	}
	action := m[1]
	e.expectRedirect("POST", action, nil, sc+"#modifications")
	if body := e.expectContains(sc, "Modification retirée"); strings.Contains(body, "Apport") {
		t.Error("modification retirée encore affichée")
	}
	e.expectRedirect("POST", sc, url.Values{"name": {"Achat voiture"}}, sc)
	e.expectRedirect("POST", sc+"/convertir", nil, "/previsionnel")
	e.expectContains("/previsionnel", "Scénario converti en données réelles")
	e.expectContains("/credits", "Voiture")
	e.expectContains("/recurrences", "Crédit Voiture")
	if res := e.do("GET", sc, nil); res.status != http.StatusNotFound {
		t.Errorf("scénario converti : %d", res.status)
	}

	other := e.created("/scenarios", url.Values{"name": {"Rien"}}, "/scenarios/")
	e.expectRedirect("POST", other+"/supprimer", nil, "/scenarios")
	e.expectContains("/scenarios", "Scénario supprimé")
}
