package web

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/codecolors/buddyget/internal/ledger"
)

// loggedIn renvoie un environnement avec un utilisateur connecté.
func loggedIn(t *testing.T) *testEnv {
	t.Helper()
	e := newTestEnv(t)
	e.createUser()
	e.login()
	return e
}

func (e *testEnv) expectFlash(path, msg string) string {
	e.t.Helper()
	res := e.do("GET", path, nil)
	if !strings.Contains(res.body, msg) {
		e.t.Fatalf("message %q absent de %s", msg, path)
	}
	return res.body
}

func (e *testEnv) createAccount(name, balance string) {
	e.t.Helper()
	e.expectRedirect("POST", "/comptes", url.Values{
		"name": {name}, "kind": {"courant"}, "bank": {"SG"},
		"opening_balance": {balance}, "opening_date": {"2026-01-01"},
	}, "/comptes")
}

// idOf extrait l'identifiant du premier lien /<section>/<id> qui précède le
// texte donné.
func idOf(t *testing.T, body, section, text string) string {
	t.Helper()
	m := regexp.MustCompile(`href="/` + section + `/(\d+)"[^<]*>(?:\s*<[^>]+>)*\s*` + regexp.QuoteMeta(text)).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("lien vers %q introuvable", text)
	}
	return m[1]
}

func TestAccountsScreens(t *testing.T) {
	e := loggedIn(t)
	if body := e.do("GET", "/", nil).body; !strings.Contains(body, "Créer un compte") {
		t.Error("le tableau de bord vide doit proposer de créer un compte")
	}

	res := e.do("POST", "/comptes", url.Values{"name": {""}, "kind": {"courant"}, "bank": {"SG"}, "opening_balance": {"0"}, "opening_date": {"2026-01-01"}})
	if res.status != http.StatusUnprocessableEntity || !strings.Contains(res.body, "Indiquez le nom du compte.") {
		t.Errorf("nom manquant : %d", res.status)
	}
	res = e.do("POST", "/comptes", url.Values{"name": {"Joint"}, "kind": {"courant"}, "bank": {"SG"}, "opening_balance": {"douze"}, "opening_date": {"2026-01-01"}})
	if res.status != http.StatusUnprocessableEntity || !strings.Contains(res.body, `value="Joint"`) {
		t.Errorf("solde invalide : la saisie doit être conservée (%d)", res.status)
	}

	e.createAccount("Compte joint", "1 234,50")
	body := e.expectFlash("/comptes", "Compte enregistré.")
	if !strings.Contains(body, "1\u202f234,50\u00a0€") {
		t.Error("le solde doit être affiché au format français")
	}
	if !strings.Contains(e.do("GET", "/", nil).body, "Compte joint") {
		t.Error("le tableau de bord doit lister le compte")
	}

	id := idOf(t, body, "comptes", "Compte joint")
	if res := e.do("GET", "/comptes/"+id, nil); !strings.Contains(res.body, `value="1234,50"`) {
		t.Error("le formulaire de modification doit être prérempli")
	}
	e.expectRedirect("POST", "/comptes/"+id+"/archiver", url.Values{"archived": {"true"}}, "/comptes")
	if body := e.expectFlash("/comptes", "Compte archivé."); !strings.Contains(body, "Comptes archivés") {
		t.Error("le compte doit apparaître parmi les archivés")
	}
	e.expectRedirect("POST", "/comptes/"+id+"/supprimer", nil, "/comptes")
	e.expectFlash("/comptes", "Compte supprimé.")
	if res := e.do("GET", "/comptes/"+id, nil); res.status != http.StatusNotFound {
		t.Errorf("compte supprimé : %d, attendu 404", res.status)
	}
}

func TestCategoriesScreens(t *testing.T) {
	e := loggedIn(t)
	body := e.do("GET", "/categories", nil).body
	for _, want := range []string{"Dépenses", "Revenus", "Virements", "Alimentation", `<circle cx="5" cy="5" r="5" fill="#43a047"/>`} {
		if !strings.Contains(body, want) {
			t.Errorf("%q absent de la page des catégories", want)
		}
	}
	parent := idOf(t, body, "categories", "Loisirs")
	e.expectRedirect("POST", "/categories", url.Values{"name": {"Cinéma"}, "parent": {parent}, "color": {"#112233"}}, "/categories")
	e.expectFlash("/categories", "Catégorie enregistrée.")

	res := e.do("POST", "/categories", url.Values{"name": {"cinéma"}, "parent": {parent}})
	if res.status != http.StatusUnprocessableEntity || !strings.Contains(res.body, "déjà ce nom") {
		t.Errorf("doublon : %d", res.status)
	}
	if res := e.do("GET", "/categories/nouvelle?parent="+parent, nil); !strings.Contains(res.body, `value="`+parent+`" selected`) {
		t.Error("le lien « Ajouter une sous-catégorie » doit présélectionner le parent")
	}
}

func TestOperationEntry(t *testing.T) {
	e := loggedIn(t)
	if res := e.do("GET", "/operations/nouvelle", nil); !strings.Contains(res.body, "Créez d'abord un compte") {
		t.Error("sans compte, la saisie doit inviter à en créer un")
	}
	e.createAccount("Courant", "100")
	e.createAccount("Livret", "0")
	accounts := e.do("GET", "/comptes", nil).body
	courant, livret := idOf(t, accounts, "comptes", "Courant"), idOf(t, accounts, "comptes", "Livret")

	form := e.do("GET", "/operations/nouvelle", nil).body
	for _, want := range []string{`inputmode="decimal"`, `name="amount"`, `autofocus`, `value="depense" checked`, `<datalist id="libelles">`} {
		if !strings.Contains(form, want) {
			t.Errorf("formulaire de saisie : %q absent", want)
		}
	}

	// Dépense, puis « Enregistrer et saisir une autre » qui garde le contexte.
	e.expectRedirect("POST", "/operations", url.Values{
		"type": {"depense"}, "amount": {"12,50"}, "label": {"Boulangerie"}, "date": {"2026-10-30"},
		"account": {courant}, "back": {"/operations"},
	}, "/operations")
	e.expectFlash("/operations", "Opération enregistrée.")
	next := "/operations/nouvelle?" + url.Values{"compte": {courant}, "date": {"2026-10-28"}, "retour": {"/operations"}, "type": {"revenu"}}.Encode()
	e.expectRedirect("POST", "/operations", url.Values{
		"type": {"revenu"}, "amount": {"2500"}, "label": {"Salaire"}, "date": {"2026-10-28"},
		"account": {courant}, "next": {"nouvelle"}, "back": {"/operations"},
	}, next)
	form = e.do("GET", next, nil).body
	if !strings.Contains(form, `value="revenu" checked`) || !strings.Contains(form, `value="2026-10-28"`) || !strings.Contains(form, `<option value="Salaire">`) {
		t.Error("la saisie suivante doit reprendre type, date et proposer les libellés récents")
	}

	// Virement, puis erreurs de saisie (le formulaire est réaffiché).
	e.expectRedirect("POST", "/operations", url.Values{
		"type": {"virement"}, "amount": {"200"}, "label": {"Épargne"}, "date": {"2026-10-29"},
		"account": {courant}, "to_account": {livret}, "category": {"1"}, "back": {"/operations"},
	}, "/operations")
	e.expectFlash("/operations", "Virement enregistré.")
	for _, c := range []struct{ amount, msg string }{{"", "Indiquez un montant."}, {"0", "non nul"}, {"12,345", "deux décimales"}} {
		res := e.do("POST", "/operations", url.Values{"type": {"depense"}, "amount": {c.amount}, "label": {"Test"}, "date": {"2026-10-29"}, "account": {courant}})
		if res.status != http.StatusUnprocessableEntity || !strings.Contains(res.body, c.msg) || !strings.Contains(res.body, `value="Test"`) {
			t.Errorf("montant %q : %d", c.amount, res.status)
		}
	}

	list := e.do("GET", "/operations", nil).body
	// html/template écrit « + » sous la forme &#43;.
	for _, want := range []string{"Boulangerie", "\u221212,50\u00a0€", "&#43;2\u202f500,00\u00a0€", "Virement vers Livret", "Virement depuis Courant", "4 opérations"} {
		if !strings.Contains(list, want) {
			t.Errorf("liste : %q absent", want)
		}
	}

	// Modification d'un virement depuis sa jambe d'arrivée : le sens est conservé.
	leg := idOf(t, list, "operations", "Épargne")
	edit := e.do("GET", "/operations/"+leg, nil).body
	if !strings.Contains(edit, `name="type" value="virement"`) || !strings.Contains(edit, "Modifier le virement") {
		t.Error("une jambe de virement s'édite comme un virement")
	}
	e.expectRedirect("POST", "/operations/"+leg, url.Values{
		"type": {"depense"}, "amount": {"150"}, "label": {"Épargne"}, "date": {"2026-10-29"},
		"account": {courant}, "to_account": {livret}, "back": {"/operations"},
	}, "/operations")
	if body := e.do("GET", "/comptes", nil).body; !strings.Contains(body, "150,00\u00a0€") {
		t.Error("le virement modifié doit créditer le livret de 150 €")
	}

	e.expectRedirect("POST", "/operations/"+leg+"/supprimer", url.Values{"back": {"/operations"}}, "/operations")
	if body := e.expectFlash("/operations", "Opération supprimée."); strings.Contains(body, "Virement vers") {
		t.Error("les deux jambes du virement doivent être supprimées")
	}
}

func TestOperationsSearch(t *testing.T) {
	e := loggedIn(t)
	e.createAccount("Courant", "0")
	courant := idOf(t, e.do("GET", "/comptes", nil).body, "comptes", "Courant")
	for _, l := range []string{"Café de la Gare", "Supermarché", "Café du Port"} {
		e.expectRedirect("POST", "/operations", url.Values{"type": {"depense"}, "amount": {"3"}, "label": {l}, "date": {"2026-10-01"}, "account": {courant}, "back": {"/operations"}}, "/operations")
	}

	page := e.do("GET", "/operations?q=cafe", nil)
	if !strings.Contains(page.body, "<html") || !strings.Contains(page.body, "2 opérations") || strings.Contains(page.body, "Supermarché</span>") {
		t.Errorf("recherche : page complète attendue avec 2 résultats")
	}
	if page.header.Get("Vary") != "HX-Request" {
		t.Error("la réponse varie selon HX-Request")
	}
	partial := e.do("GET", "/operations?q=cafe+port", nil, "HX-Request", "true", "HX-Target", "resultats")
	if strings.Contains(partial.body, "<html") || !strings.HasPrefix(strings.TrimSpace(partial.body), `<div id="resultats"`) || !strings.Contains(partial.body, "1 opération") {
		t.Errorf("requête HTMX : seul le bloc des résultats est attendu :\n%.300s", partial.body)
	}
	if body := e.do("GET", "/operations?du=pas-une-date", nil).body; !strings.Contains(body, "date de la recherche est invalide") {
		t.Error("un critère invalide doit être signalé")
	}
	if body := e.do("GET", "/operations?q=introuvable", nil).body; !strings.Contains(body, "Aucune opération ne correspond") {
		t.Error("message de recherche vide attendu")
	}
}

func TestBatchEdit(t *testing.T) {
	e := loggedIn(t)
	e.createAccount("Courant", "0")
	courant := idOf(t, e.do("GET", "/comptes", nil).body, "comptes", "Courant")
	for _, l := range []string{"Café A", "Café B", "Loyer"} {
		e.expectRedirect("POST", "/operations", url.Values{"type": {"depense"}, "amount": {"3"}, "label": {l}, "date": {"2026-10-01"}, "account": {courant}, "back": {"/operations"}}, "/operations")
	}
	restaurants := idOf(t, e.do("GET", "/categories", nil).body, "categories", "Restaurants")

	// Tous les résultats d'une recherche ; retour sur la même recherche.
	filtre := url.Values{"q": {"café"}}.Encode()
	e.expectRedirect("POST", "/operations/lot", url.Values{"portee": {"tout"}, "filtre": {filtre}, "categorie": {restaurants}}, "/operations?"+filtre)
	body := e.expectFlash("/operations?"+filtre, "2 opérations modifiées.")
	if strings.Count(body, "Alimentation › Restaurants</span>") != 2 {
		t.Error("les deux cafés doivent être catégorisés")
	}

	// Sélection explicite sans modification : message d'erreur, rien ne change.
	loyer := idOf(t, body+e.do("GET", "/operations", nil).body, "operations", "Loyer")
	e.expectRedirect("POST", "/operations/lot", url.Values{"ids": {loyer}}, "/operations")
	e.expectFlash("/operations", "Choisissez une modification à appliquer.")
	e.expectRedirect("POST", "/operations/lot", url.Values{"ids": {loyer}, "categorie": {"aucune"}}, "/operations")
	e.expectFlash("/operations", "1 opération modifiée.")
}

func TestParseFilter(t *testing.T) {
	f, problem := parseFilter(url.Values{
		"q": {"loyer"}, "compte": {"3"}, "categorie": {"aucune"}, "du": {"2026-01-01"}, "au": {"31/12/2026"},
		"min": {"-10,50"}, "max": {"abc"}, "pointage": {"non"}, "tri": {"montant"},
	})
	want := ledger.Filter{Query: "loyer", AccountID: 3, CategoryID: ledger.Uncategorized, From: "2026-01-01",
		MinAmount: 1050, Cleared: ledger.ClearedNo, Sort: ledger.SortAmountAsc}
	if f != want {
		t.Errorf("filtre = %+v\nattendu  %+v", f, want)
	}
	if problem == "" {
		t.Error("les critères invalides doivent être signalés")
	}
}

func TestStaticGzip(t *testing.T) {
	e := newTestEnv(t)
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, e.srv.URL+"/static/htmx.min.js", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Header.Get("Content-Encoding") != "gzip" || len(body) > 20_000 || !bytes.HasPrefix(body, []byte{0x1f, 0x8b}) {
		t.Errorf("htmx compressé attendu : %q, %d octets", resp.Header.Get("Content-Encoding"), len(body))
	}
}
