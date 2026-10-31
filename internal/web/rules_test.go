package web

import (
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// operation saisit une dépense et renvoie son identifiant.
func (e *testEnv) operation(account, label, amount, date string) string {
	e.t.Helper()
	e.expectRedirect("POST", "/operations", url.Values{
		"type": {"depense"}, "amount": {amount}, "label": {label}, "date": {date}, "account": {account}, "back": {"/operations"},
	}, "/operations")
	return idOf(e.t, e.do("GET", "/operations?q="+url.QueryEscape(label), nil).body, "operations", label)
}

func TestRuleInOneClick(t *testing.T) {
	e := loggedIn(t)
	e.createAccount("Courant", "0")
	account := idOf(t, e.do("GET", "/comptes", nil).body, "comptes", "Courant")
	op := e.operation(account, "CARTE X4321 30/10 BOULANGERIE MARTIN 75 PARIS", "4,20", "2026-10-30")
	e.operation(account, "CARTE X4321 02/11 BOULANGERIE MARTIN 75 PARIS", "3,10", "2026-11-02")
	courses := idOf(t, e.do("GET", "/categories", nil).body, "categories", "Courses")

	page := e.do("GET", "/operations/"+op, nil, "Referer", e.srv.URL+"/operations?q=boulangerie").body
	for _, want := range []string{"Catégoriser automatiquement", `name="pattern" value="BOULANGERIE MARTIN"`, `id="rule-category"`} {
		if !strings.Contains(page, want) {
			t.Errorf("page de l'opération : %q absent", want)
		}
	}

	e.expectRedirect("POST", "/regles", url.Values{
		"pattern": {"BOULANGERIE MARTIN"}, "match": {"contient"}, "category": {courses}, "apply": {"1"}, "back": {"/operations?q=boulangerie"},
	}, "/operations?q=boulangerie")
	body := e.expectFlash("/operations?q=boulangerie", "Règle enregistrée. 2 opérations catégorisées.")
	if strings.Count(body, "Alimentation › Courses</span>") != 2 {
		t.Error("les deux boulangeries doivent être catégorisées")
	}

	rules := e.do("GET", "/regles", nil).body
	if !strings.Contains(rules, "Libellé contient « BOULANGERIE MARTIN »") || !strings.Contains(rules, "Alimentation › Courses") {
		t.Error("la règle doit apparaître dans la liste")
	}
	rule := idOf(t, rules, "regles", "Libellé contient")
	if res := e.do("POST", "/regles/"+rule, url.Values{"pattern": {"(x"}, "match": {"regex"}, "category": {courses}}); res.status != 422 || !strings.Contains(res.body, "Expression régulière invalide.") {
		t.Errorf("expression invalide : %d", res.status)
	}
	e.expectRedirect("POST", "/regles/"+rule+"/supprimer", nil, "/regles")
	e.expectFlash("/regles", "Règle supprimée.")
	if res := e.do("GET", "/regles/"+rule, nil); res.status != 404 {
		t.Errorf("règle supprimée : %d", res.status)
	}
}

func TestBatchEditCreatesRule(t *testing.T) {
	e := loggedIn(t)
	e.createAccount("Courant", "0")
	account := idOf(t, e.do("GET", "/comptes", nil).body, "comptes", "Courant")
	e.operation(account, "Café de la Gare", "3,50", "2026-10-01")
	restaurants := idOf(t, e.do("GET", "/categories", nil).body, "categories", "Restaurants")

	filtre := url.Values{"q": {"café"}}.Encode()
	e.expectRedirect("POST", "/operations/lot", url.Values{"portee": {"tout"}, "filtre": {filtre}, "categorie": {restaurants}, "regle": {"1"}}, "/operations?"+filtre)
	e.expectFlash("/operations?"+filtre, "1 opération modifiée. Règle « café » créée pour les prochaines opérations.")
	if !strings.Contains(e.do("GET", "/regles", nil).body, "« café »") {
		t.Error("la règle doit être créée")
	}

	e.expectRedirect("POST", "/operations/lot", url.Values{"portee": {"tout"}, "filtre": {""}, "categorie": {restaurants}, "regle": {"1"}}, "/operations")
	e.expectFlash("/operations", "Règle non créée : recherchez d&#39;abord un texte dans les libellés.") // apostrophe échappée
}

func TestTransferDetection(t *testing.T) {
	e := loggedIn(t)
	e.createAccount("Courant", "0")
	e.createAccount("Livret", "0")
	accounts := e.do("GET", "/comptes", nil).body
	courant, livret := idOf(t, accounts, "comptes", "Courant"), idOf(t, accounts, "comptes", "Livret")
	e.operation(courant, "VIR EPARGNE", "200", "2026-10-10")
	e.expectRedirect("POST", "/operations", url.Values{
		"type": {"revenu"}, "amount": {"200"}, "label": {"VIR RECU"}, "date": {"2026-10-11"}, "account": {livret}, "back": {"/operations"},
	}, "/operations")

	if body := e.do("GET", "/operations", nil).body; !strings.Contains(body, "1 virement interne possible à vérifier") {
		t.Fatal("la liste doit signaler le virement à rapprocher")
	}
	page := e.do("GET", "/operations/virements", nil).body
	pair := regexp.MustCompile(`name="debit" value="(\d+)"><input type="hidden" name="credit" value="(\d+)"`).FindStringSubmatch(page)
	if pair == nil || !strings.Contains(page, "VIR EPARGNE") || !strings.Contains(page, "VIR RECU") {
		t.Fatal("la paire doit être proposée")
	}
	e.expectRedirect("POST", "/operations/virements/relier", url.Values{"debit": {pair[1]}, "credit": {pair[2]}}, "/operations/virements")
	e.expectFlash("/operations/virements", "Virement relié")
	list := e.do("GET", "/operations", nil).body
	if strings.Contains(list, "virement interne possible") || !strings.Contains(list, "Virement vers Livret") {
		t.Error("le virement relié doit s'afficher comme tel")
	}

	// Défaire : la paire n'est plus proposée.
	edit := e.do("GET", "/operations/"+pair[1], nil).body
	if !strings.Contains(edit, "Défaire le virement") || strings.Contains(edit, "Catégoriser automatiquement") {
		t.Error("une jambe de virement propose de défaire le virement, pas de règle")
	}
	e.expectRedirect("POST", "/operations/"+pair[1]+"/delier", url.Values{"back": {"/operations"}}, "/operations")
	body := e.expectFlash("/operations", "Virement défait")
	if strings.Contains(body, "Virement vers") || strings.Contains(body, "virement interne possible") {
		t.Error("après déliaison, ni virement ni proposition")
	}

	// Écarter une autre paire.
	e.operation(courant, "Remboursement ami", "50", "2026-10-20")
	e.expectRedirect("POST", "/operations", url.Values{
		"type": {"revenu"}, "amount": {"50"}, "label": {"Cadeau"}, "date": {"2026-10-20"}, "account": {livret}, "back": {"/operations"},
	}, "/operations")
	pair = regexp.MustCompile(`name="debit" value="(\d+)"><input type="hidden" name="credit" value="(\d+)"`).FindStringSubmatch(e.do("GET", "/operations/virements", nil).body)
	e.expectRedirect("POST", "/operations/virements/ignorer", url.Values{"debit": {pair[1]}, "credit": {pair[2]}}, "/operations/virements")
	if body := e.expectFlash("/operations/virements", "Paire écartée"); !strings.Contains(body, "Aucun virement à rapprocher.") {
		t.Error("la paire écartée ne doit plus être proposée")
	}
}

func TestImportPreviewShowsSuggestedCategory(t *testing.T) {
	e := loggedIn(t)
	e.createAccount("Courant", "0")
	account := idOf(t, e.do("GET", "/comptes", nil).body, "comptes", "Courant")
	energy := idOf(t, e.do("GET", "/categories", nil).body, "categories", "Énergie")
	e.expectRedirect("POST", "/regles", url.Values{"pattern": {"edf"}, "match": {"contient"}, "category": {energy}, "back": {"/regles"}}, "/regles")

	draft := e.upload(account, "releve.csv", fixture(t, "sg/releve-octobre.csv")).header.Get("Location")
	preview := e.do("GET", draft, nil).body
	if !strings.Contains(preview, "Logement › Énergie") || !strings.Contains(preview, "Nouvelle, sans catégorie") {
		t.Error("l'aperçu doit montrer la catégorie suggérée")
	}
}
