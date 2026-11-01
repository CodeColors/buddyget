package web

import (
	"archive/zip"
	"bytes"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/platform/money"
)

// Écrans du jalon 11 : partage du foyer, exports, sauvegardes.

func TestHouseholdScreens(t *testing.T) {
	e := loggedIn(t)
	today := string(calendar.Today())
	e.expectContains("/partage", "Ajoutez les membres du foyer")
	if res := e.do("POST", "/partage/membres", url.Values{"name": {" "}}); res.status != http.StatusUnprocessableEntity {
		t.Errorf("membre sans nom : %d", res.status)
	}
	e.expectRedirect("POST", "/partage/membres", url.Values{"name": {"Hugo"}, "income": {"3000"}}, "/partage")
	e.expectRedirect("POST", "/partage/membres", url.Values{"name": {"Léa"}, "income": {"2000"}}, "/partage")
	body := e.expectContains("/partage", "Membre ajouté", "Revenu mensuel "+money.Format(3_000_00), "personne ne doit rien")
	hugo := idOf(t, body, "partage/membres", "Hugo")
	lea := idOf(t, body, "partage/membres", "Léa")
	e.createAccount("Joint", "0")
	account := idOf(t, e.do("GET", "/comptes", nil).body, "comptes", "Joint")

	form := e.expectContains("/operations/nouvelle", "Partage du foyer", `name="payer"`, `value="prorata"`, `name="share_`+hugo+`"`)
	if !strings.Contains(form, `name="split_member" value="`+lea+`" checked`) {
		t.Error("tous les membres sont proposés par défaut")
	}
	op := url.Values{"type": {"depense"}, "amount": {"100"}, "label": {"Appartement"}, "date": {today}, "account": {account}, "back": {"/operations"},
		"shared": {"1"}, "payer": {hugo}, "split_mode": {"libre"}, "split_member": {hugo, lea}, "share_" + hugo: {"60"}, "share_" + lea: {"30"}}
	if res := e.do("POST", "/operations", op); res.status != http.StatusUnprocessableEntity || !strings.Contains(res.body, "totalisent") {
		t.Errorf("parts incohérentes : %d", res.status)
	}
	if body := e.do("GET", "/operations", nil).body; strings.Contains(body, "Appartement") {
		t.Error("une saisie refusée n'enregistre pas l'opération")
	}
	op.Set("split_mode", "prorata")
	e.expectRedirect("POST", "/operations", op, "/operations")
	e.expectContains("/partage", "Léa doit "+money.Format(40_00)+" à Hugo", "payé par Hugo", "Au prorata")

	// La modification reprend le partage enregistré.
	loyer := idOf(t, e.do("GET", "/operations", nil).body, "operations", "Appartement")
	e.expectContains("/operations/"+loyer, `name="shared" value="1" checked`, `value="prorata" checked`, `value="60,00"`)

	// Remboursement de Léa, en espèces.
	e.expectContains("/partage?de="+lea+"&a="+hugo+"&montant=40,00", `value="40,00"`)
	e.expectRedirect("POST", "/partage/remboursements", url.Values{"from": {lea}, "to": {hugo}, "amount": {"40"}, "date": {today},
		"from_account": {"0"}, "to_account": {"0"}}, "/partage")
	body = e.expectContains("/partage", "Remboursement enregistré", "personne ne doit rien", "Léa → Hugo")
	settlement := regexp.MustCompile(`/partage/remboursements/(\d+)/supprimer`).FindStringSubmatch(body)
	if settlement == nil {
		t.Fatal("annulation du remboursement introuvable")
	}
	e.expectRedirect("POST", settlement[0], nil, "/partage")
	e.expectContains("/partage", "Remboursement annulé", "Léa doit")

	// Un membre présent dans un partage ne se supprime pas.
	e.expectRedirect("POST", "/partage/membres/"+lea+"/supprimer", nil, "/partage/membres/"+lea)
	e.expectContains("/partage/membres/"+lea, "archivez-le plutôt")

	// Partage retiré à la modification.
	op.Del("shared")
	e.expectRedirect("POST", "/operations/"+loyer, op, "/operations")
	e.expectContains("/partage", "Aucune opération partagée")

	// Membre : modification, archivage ; sans partage, il se supprime.
	e.expectContains("/partage/membres/"+hugo, `value="3000,00"`)
	e.expectRedirect("POST", "/partage/membres/"+hugo, url.Values{"name": {"Hugo"}, "income": {"3500"}}, "/partage")
	e.expectRedirect("POST", "/partage/membres/"+lea+"/archiver", url.Values{"archived": {"1"}}, "/partage")
	e.expectContains("/partage", "Membre archivé", "Archivé")
	e.expectRedirect("POST", "/partage/membres/"+lea+"/supprimer", nil, "/partage")
	if body := e.expectContains("/partage", "Membre supprimé"); strings.Contains(body, "Léa") {
		t.Error("membre supprimé encore affiché")
	}
}

func TestExportScreens(t *testing.T) {
	e := loggedIn(t)
	e.createAccount("Courant", "0")
	account := idOf(t, e.do("GET", "/comptes", nil).body, "comptes", "Courant")
	today := string(calendar.Today())
	for _, op := range []url.Values{
		{"type": {"depense"}, "amount": {"42,50"}, "label": {"Pharmacie"}, "date": {today}, "account": {account}, "tags": {"déductible"}, "back": {"/operations"}},
		{"type": {"revenu"}, "amount": {"2000"}, "label": {"Salaire"}, "date": {today}, "account": {account}, "back": {"/operations"}},
	} {
		e.expectRedirect("POST", "/operations", op, "/operations")
	}
	e.expectContains("/exports", "Opérations (CSV)", "Rapports (PDF ou CSV)", "Dossier pour un comptable", `<option value="déductible">`)
	e.expectContains("/operations?q=pharma", `href="/exports/operations?q=pharma"`)
	e.expectContains("/rapports", `href="/exports/rapport?format=csv&amp;mois=`+today[:7]+`"`)

	res := e.do("GET", "/exports/operations?q=pharma", nil)
	if res.status != http.StatusOK || !strings.HasPrefix(res.header.Get("Content-Type"), "text/csv") ||
		!strings.Contains(res.header.Get("Content-Disposition"), "buddyget-operations-") ||
		!strings.Contains(res.body, "Pharmacie;-42,50") || strings.Contains(res.body, "Salaire") {
		t.Errorf("export CSV : %d %q %q", res.status, res.header, res.body)
	}
	res = e.do("GET", "/exports/rapport?mois="+today[:7], nil)
	if res.status != http.StatusOK || res.header.Get("Content-Type") != "application/pdf" || !strings.HasPrefix(res.body, "%PDF-") {
		t.Errorf("rapport PDF : %d %q", res.status, res.header)
	}
	res = e.do("GET", "/exports/rapport?format=csv&annee="+today[:4], nil)
	if !strings.Contains(res.body, "Total;Revenus;2000,00") {
		t.Errorf("rapport CSV : %q", res.body)
	}
	res = e.do("GET", "/exports/dossier?etiquette=d%C3%A9ductible&pieces=1", nil)
	z, err := zip.NewReader(strings.NewReader(res.body), int64(len(res.body)))
	if err != nil || res.header.Get("Content-Type") != "application/zip" || len(z.File) != 2 {
		t.Fatalf("dossier : %v %q", err, res.header)
	}
	if res := e.do("GET", "/exports/dossier?compte=999", nil); res.status != http.StatusNotFound {
		t.Errorf("compte inconnu : %d", res.status)
	}
}

func TestBackupScreens(t *testing.T) {
	e := loggedIn(t)
	e.expectContains("/sauvegardes", "Aucune sauvegarde", "BACKUP_PASSPHRASE", "désactivé")
	e.createAccount("Courant", "1000")
	e.expectRedirect("POST", "/sauvegardes", nil, "/sauvegardes")
	body := e.expectContains("/sauvegardes", "Sauvegarde terminée", "Télécharger", "Restaurer")
	name := regexp.MustCompile(`/sauvegardes/fichiers/(buddyget-[0-9-]+\.tar\.gz)"`).FindStringSubmatch(body)
	if name == nil {
		t.Fatal("archive introuvable")
	}
	res := e.do("GET", "/sauvegardes/fichiers/"+name[1], nil)
	if res.status != http.StatusOK || !strings.HasPrefix(res.body, "\x1f\x8b") {
		t.Fatalf("téléchargement : %d", res.status)
	}
	archive := res.body
	if res := e.do("GET", "/sauvegardes/fichiers/..%2Fbuddyget.db", nil); res.status != http.StatusNotFound {
		t.Errorf("chemin arbitraire : %d", res.status)
	}

	// Réglages : copie dans un dossier inexistant refusée, puis acceptée.
	settings := url.Values{"auto": {"1"}, "keep": {"5"}, "copy": {"dossier"}, "copy_dir": {"/n/existe/pas"}}
	if res := e.do("POST", "/sauvegardes/reglages", settings); res.status != http.StatusUnprocessableEntity || !strings.Contains(res.body, `value="/n/existe/pas"`) {
		t.Errorf("dossier inexistant : %d", res.status)
	}
	settings.Set("copy_dir", t.TempDir())
	e.expectRedirect("POST", "/sauvegardes/reglages", settings, "/sauvegardes")
	e.expectContains("/sauvegardes", "chaque nuit", `value="5"`)

	// Restauration d'une archive conservée : redémarrage demandé.
	res = e.do("POST", "/sauvegardes/restaurer?nom="+name[1], nil)
	if res.status != http.StatusOK || !strings.Contains(res.body, "Restauration en cours") || res.header.Get("Refresh") == "" || e.restarts.Load() != 1 {
		t.Errorf("restauration : %d, %d redémarrage(s)", res.status, e.restarts.Load())
	}
	if _, err := os.Stat(filepath.Join(e.dataDir, "restauration", "buddyget.db")); err != nil {
		t.Errorf("restauration préparée : %v", err)
	}
	e.expectContains("/sauvegardes", "attend le redémarrage")

	// Restauration d'un fichier déposé ; un fichier quelconque est refusé.
	for _, c := range []struct {
		data   string
		status int
	}{{archive, http.StatusOK}, {"pas une sauvegarde", http.StatusSeeOther}} {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		part, _ := mw.CreateFormFile("fichier", "sauvegarde.tar.gz")
		_, _ = part.Write([]byte(c.data))
		_ = mw.Close()
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, e.srv.URL+"/sauvegardes/restaurer", &buf)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		resp, err := e.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != c.status {
			t.Errorf("dépôt %q : %d", c.data[:5], resp.StatusCode)
		}
	}
	e.expectContains("/sauvegardes", "pas une sauvegarde BuddyGet")

	e.expectRedirect("POST", "/sauvegardes/fichiers/"+name[1]+"/supprimer", nil, "/sauvegardes")
	e.expectContains("/sauvegardes", "Sauvegarde supprimée", "Aucune sauvegarde")
}
