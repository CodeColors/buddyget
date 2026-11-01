package web

import (
	"bytes"
	"image"
	"image/png"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/codecolors/buddyget/internal/platform/calendar"
)

func TestQuickEntryTagsAndAttachments(t *testing.T) {
	e := loggedIn(t)
	e.createAccount("Courant", "1000")
	account := idOf(t, e.do("GET", "/comptes", nil).body, "comptes", "Courant")
	today := string(calendar.Today())
	e.expectRedirect("POST", "/operations", url.Values{
		"type": {"depense"}, "amount": {"42,50"}, "label": {"Hôtel"}, "date": {today}, "account": {account},
		"tags": {"vacances 2027, déductible"}, "back": {"/operations"},
	}, "/operations")
	list := e.do("GET", "/operations", nil).body
	op := idOf(t, list, "operations", "Hôtel")
	if !strings.Contains(list, `<span class="badge badge-tag">déductible</span>`) || !strings.Contains(list, `<option value="vacances 2027">`) {
		t.Error("étiquettes affichées et proposées au filtre")
	}
	if body := e.do("GET", "/operations?etiquette=d%C3%A9ductible", nil).body; !strings.Contains(body, "1 opération") {
		t.Error("filtre par étiquette")
	}
	e.expectRedirect("POST", "/operations/lot", url.Values{"portee": {"tout"}, "filtre": {""}, "ajouter_etiquette": {"Été"}}, "/operations")
	if body := e.do("GET", "/etiquettes", nil).body; !strings.Contains(body, "Été") || !strings.Contains(body, "vacances 2027") {
		t.Error("page des étiquettes")
	}

	// Dupliquer, puis faire un modèle et s'en servir.
	form := e.do("GET", "/operations/nouvelle?dupliquer=derniere", nil).body
	for _, want := range []string{`value="42,50"`, `value="Hôtel"`, `value="déductible, Été, vacances 2027"`, `name="date" type="date" value="` + today + `"`} {
		if !strings.Contains(form, want) {
			t.Errorf("duplication : %q absent", want)
		}
	}
	e.expectRedirect("POST", "/operations/"+op+"/modele", url.Values{"name": {"Nuit d'hôtel"}}, "/operations/"+op)
	form = e.do("GET", "/operations/nouvelle", nil).body
	tpl := regexpFind(t, form, `action="/modeles/(\d+)/utiliser"`)
	e.expectRedirect("POST", "/modeles/"+tpl+"/utiliser", nil, "/operations")
	if body := e.expectFlash("/operations", "Opération « Hôtel » enregistrée."); !strings.Contains(body, "2 opérations") {
		t.Error("le modèle crée l'opération du jour")
	}
	e.expectRedirect("POST", "/modeles/"+tpl+"/supprimer", nil, "/operations/nouvelle")

	// Pièces jointes : une photo réduite, un PDF, un format refusé.
	var img bytes.Buffer
	_ = png.Encode(&img, image.NewRGBA(image.Rect(0, 0, 2000, 100)))
	if res := e.postFile("/operations/"+op+"/pieces", nil, "ticket.png", img.Bytes()); res.status != http.StatusSeeOther {
		t.Fatalf("envoi de la photo : %d", res.status)
	}
	e.postFile("/operations/"+op+"/pieces", nil, "facture.pdf", []byte("%PDF-1.4\n"))
	e.postFile("/operations/"+op+"/pieces", nil, "notes.txt", []byte("texte"))
	page := e.expectFlash("/operations/"+op, "Format non pris en charge")
	photo := regexpFind(t, page, `href="/pieces/(\d+)">ticket.png`)
	pdf := regexpFind(t, page, `href="/pieces/(\d+)">facture.pdf`)
	if res := e.do("GET", "/pieces/"+photo, nil); res.header.Get("Content-Type") != "image/jpeg" || !strings.HasPrefix(res.header.Get("Content-Disposition"), "inline") {
		t.Errorf("photo servie : %v", res.header)
	}
	if res := e.do("GET", "/pieces/"+pdf, nil); !strings.HasPrefix(res.header.Get("Content-Disposition"), "attachment") || res.body != "%PDF-1.4\n" {
		t.Errorf("PDF servi : %v", res.header)
	}
	e.expectRedirect("POST", "/pieces/"+pdf+"/supprimer", url.Values{"back": {"/operations/" + op}}, "/operations/"+op)
	if res := e.do("GET", "/pieces/"+pdf, nil); res.status != http.StatusNotFound {
		t.Errorf("pièce supprimée : %d", res.status)
	}
}

func TestSubscriptionsAndReports(t *testing.T) {
	e := loggedIn(t)
	e.createAccount("Courant", "1000")
	account := idOf(t, e.do("GET", "/comptes", nil).body, "comptes", "Courant")
	if body := e.do("GET", "/abonnements", nil).body; !strings.Contains(body, "Aucun abonnement suivi") {
		t.Error("abonnements vides")
	}
	e.expectRedirect("POST", "/recurrences", url.Values{
		"type": {"depense"}, "amount": {"17,99"}, "label": {"Netflix"}, "period": {"1-mois"}, "business_day": {"suivant"},
		"start": {string(calendar.Today().AddDays(3))}, "account": {account}, "subscription": {"1"},
	}, "/recurrences")
	if body := e.do("GET", "/recurrences", nil).body; !strings.Contains(body, `<span class="badge">Abonnement</span>`) {
		t.Error("badge abonnement")
	}
	if body := e.do("GET", "/abonnements", nil).body; !strings.Contains(body, "Netflix") || !strings.Contains(body, "215,88 €") {
		t.Error("abonnement suivi, coût annuel")
	}

	e.expectRedirect("POST", "/operations", url.Values{
		"type": {"revenu"}, "amount": {"2000"}, "label": {"Salaire"}, "date": {string(calendar.Today())}, "account": {account}, "back": {"/operations"},
	}, "/operations")
	e.expectRedirect("POST", "/operations", url.Values{
		"type": {"depense"}, "amount": {"500"}, "label": {"CB CARREFOUR"}, "date": {string(calendar.Today())}, "account": {account}, "back": {"/operations"},
	}, "/operations")
	monthly := e.do("GET", "/rapports", nil).body
	for _, want := range []string{calendar.Today().MonthLabel(), "Taux d'épargne", "75 %", "CB CARREFOUR", "Sans catégorie", `href="/rapports?mois=`} {
		if !strings.Contains(monthly, want) {
			t.Errorf("rapport mensuel : %q absent", want)
		}
	}
	year := string(calendar.Today())[:4]
	if body := e.do("GET", "/rapports?annee="+year, nil).body; !strings.Contains(body, `id="mois-titre"`) || !strings.Contains(body, `aria-current="page">Annuel`) {
		t.Error("rapport annuel")
	}
}
