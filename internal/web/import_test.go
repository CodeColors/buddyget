package web

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
)

// upload dépose un fichier par le formulaire d'import (compte puis fichier).
func (e *testEnv) upload(account, name string, data []byte) result {
	e.t.Helper()
	return e.postFile("/import", url.Values{"account": {account}}, name, data)
}

// postFile envoie un formulaire multipart : les champs, puis le fichier.
func (e *testEnv) postFile(path string, fields url.Values, name string, data []byte) result {
	e.t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for k, vs := range fields {
		for _, v := range vs {
			if err := mw.WriteField(k, v); err != nil {
				e.t.Fatal(err)
			}
		}
	}
	fw, err := mw.CreateFormFile("file", name)
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err := fw.Write(data); err != nil {
		e.t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		e.t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(e.t.Context(), http.MethodPost, e.srv.URL+path, &body)
	if err != nil {
		e.t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := e.client.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return result{status: resp.StatusCode, header: resp.Header, cookies: resp.Cookies(), body: string(b)}
}

func fixture(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile("../../testdata/" + path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

var draftPath = regexp.MustCompile(`^/import/[A-Z2-7]{26}$`)

func TestImportWizard(t *testing.T) {
	e := loggedIn(t)
	e.createAccount("Courant", "1000")
	account := idOf(t, e.do("GET", "/comptes", nil).body, "comptes", "Courant")

	if body := e.do("GET", "/import?compte="+account, nil).body; !strings.Contains(body, `value="`+account+`" selected`) || !strings.Contains(body, `enctype="multipart/form-data"`) {
		t.Error("étape 1 : formulaire de dépôt attendu, compte présélectionné")
	}
	res := e.upload(account, "releve-octobre.csv", fixture(t, "sg/releve-octobre.csv"))
	draft := res.header.Get("Location")
	if res.status != http.StatusSeeOther || !draftPath.MatchString(draft) {
		t.Fatalf("dépôt : %d → %q", res.status, draft)
	}

	preview := e.do("GET", draft, nil).body
	for _, want := range []string{"Vérifier l'import", "Société Générale", "<strong>6</strong> nouvelles", "Importer 6 opérations", "CARTE X4321 30/10 BOULANGERIE MARTIN 75 PARIS", `aria-current="step"><span class="step-num">2</span>`} {
		if !strings.Contains(preview, want) {
			t.Errorf("aperçu : %q absent", want)
		}
	}

	// Décocher la ligne 9 (chèque) parmi les lignes affichées.
	shown := url.Values{"shown": {"4", "5", "6", "7", "8", "9"}, "checked": {"4", "5", "6", "7", "8"}}
	e.expectRedirect("POST", draft+"/selection", shown, draft)
	if body := e.do("GET", draft+"?statut=nouvelle", nil).body; !strings.Contains(body, "Importer 5 opérations") {
		t.Error("la sélection doit être enregistrée")
	}

	e.expectRedirect("POST", draft+"/valider", nil, "/comptes/"+account+"/rapprochement?import=1")
	body := e.expectFlash("/comptes/"+account+"/rapprochement?import=1", "5 opérations importées.")
	if !strings.Contains(body, `aria-current="step"><span class="step-num">3</span>`) {
		t.Error("le rapprochement est la troisième étape de l'import")
	}

	// Rapprochement : 1 000 − 4,20 × 2 − 78,32 + 2 450 − 3,50 = 3 359,78.
	e.expectRedirect("POST", "/comptes/"+account+"/rapprochement", url.Values{"balance": {"3 239,78"}, "date": {"2026-10-31"}, "import": {"1"}}, "/comptes/"+account+"/rapprochement?import=1")
	body = e.expectFlash("/comptes/"+account+"/rapprochement?import=1", "Solde bancaire enregistré.")
	for _, want := range []string{"3 359,78 €", "−120,00 €", "Reste à pointer", "Solde pointé"} {
		if !strings.Contains(body, want) {
			t.Errorf("rapprochement : %q absent", want)
		}
	}
	if n := strings.Count(body, `class="tx pointage"`); n != 5 {
		t.Errorf("%d opérations à pointer, attendu 5", n)
	}

	// Pointage d'un geste (HTMX) : la ligne et l'écart sont renvoyés.
	reconcile := "/comptes/" + account + "/rapprochement"
	op := regexp.MustCompile(`action="/operations/(\d+)/pointer"`).FindStringSubmatch(body)[1]
	res = e.do("POST", "/operations/"+op+"/pointer", url.Values{"cleared": {"1"}, "back": {reconcile}}, "HX-Request", "true")
	if res.status != http.StatusOK || !strings.Contains(res.body, `aria-pressed="true"`) || !strings.Contains(res.body, `id="ecart" hx-swap-oob="true"`) {
		t.Errorf("pointage HTMX : %d", res.status)
	}
	// Sans script, retour à la page : l'opération pointée n'y figure plus.
	e.expectRedirect("POST", "/operations/"+op+"/pointer", url.Values{"cleared": {"1"}, "back": {reconcile}}, reconcile)
	if body = e.expectFlash(reconcile, "Opération pointée."); strings.Count(body, `class="tx pointage"`) != 4 {
		t.Error("l'opération pointée quitte la liste")
	}

	// Historique et annulation.
	history := e.do("GET", "/import/historique", nil).body
	if !strings.Contains(history, "releve-octobre.csv") || !strings.Contains(history, "5 ajoutées sur 6 lignes") {
		t.Errorf("historique incomplet")
	}
	undo := regexp.MustCompile(`action="(/import/historique/\d+/annuler)"`).FindStringSubmatch(history)
	e.expectRedirect("POST", undo[1], nil, "/import/historique")
	if body := e.expectFlash("/import/historique", "Import annulé : 5 opérations supprimées."); !strings.Contains(body, "Annulé") {
		t.Error("l'import annulé doit être signalé")
	}
	if body := e.do("GET", "/operations?compte="+account, nil).body; !strings.Contains(body, "0 opération") {
		t.Error("les opérations de l'import annulé doivent disparaître")
	}
	if res := e.do("POST", undo[1], nil); res.header.Get("Location") != "/import/historique" {
		t.Errorf("seconde annulation : %d", res.status)
	}
}

func TestImportManualMapping(t *testing.T) {
	e := loggedIn(t)
	e.createAccount("Courant", "0")
	account := idOf(t, e.do("GET", "/comptes", nil).body, "comptes", "Courant")
	draft := e.upload(account, "sans-entete.csv", fixture(t, "generique/sans-entete.csv")).header.Get("Location")

	page := e.do("GET", draft, nil).body
	for _, want := range []string{"Colonnes du fichier", "Aucun profil connu", "<td>Loyer octobre</td>", "C — -750.00"} {
		if !strings.Contains(page, want) {
			t.Errorf("correspondance : %q absent", want)
		}
	}
	form := url.Values{"header_line": {"0"}, "date": {"0"}, "label": {"1"}, "label2": {"-1"}, "amount_mode": {"montant"}, "amount": {"-1"}}
	if res := e.do("POST", draft+"/colonnes", form); res.status != http.StatusUnprocessableEntity || !strings.Contains(res.body, "colonne du montant") {
		t.Errorf("montant manquant : %d", res.status)
	}
	form.Set("amount", "2")
	form.Set("profile_name", "Sans en-tête")
	if res := e.do("POST", draft+"/colonnes", form); res.status != http.StatusUnprocessableEntity || !strings.Contains(res.body, "ligne d'en-tête") {
		t.Errorf("profil sans en-tête : %d", res.status)
	}
	form.Del("profile_name")
	e.expectRedirect("POST", draft+"/colonnes", form, draft)
	if body := e.do("GET", draft, nil).body; !strings.Contains(body, "Importer 3 opérations") || !strings.Contains(body, "Correspondance manuelle") {
		t.Error("l'aperçu doit suivre la correspondance manuelle")
	}
	e.expectRedirect("POST", draft+"/abandonner", nil, "/import")
	e.expectFlash("/import", "Import abandonné.")
	e.expectRedirect("GET", draft, nil, "/import")
}

func TestImportRefusals(t *testing.T) {
	e := loggedIn(t)
	e.createAccount("Courant", "0")
	account := idOf(t, e.do("GET", "/comptes", nil).body, "comptes", "Courant")
	for name, c := range map[string]struct {
		account, file string
		data          []byte
		msg           string
	}{
		"page HTML":       {account, "releve.xls", []byte("<html><body>relevé</body></html>"), "page web"},
		"fichier vide":    {account, "vide.csv", nil, "Le fichier est vide."},
		"trop volumineux": {account, "gros.csv", bytes.Repeat([]byte("x"), 2<<20), "taille maximale de 1 Mo"},
		"compte absent":   {"", "releve.csv", []byte("a;b\n"), "Choisissez le compte"},
	} {
		t.Run(name, func(t *testing.T) {
			res := e.upload(c.account, c.file, c.data)
			if res.status != http.StatusUnprocessableEntity || !strings.Contains(res.body, c.msg) {
				t.Errorf("%d, message %q attendu", res.status, c.msg)
			}
		})
	}
}
