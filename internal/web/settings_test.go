package web

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

func TestUserScreens(t *testing.T) {
	e := loggedIn(t)
	e.expectContains("/parametres", `href="/utilisateurs"`, `href="/sauvegardes"`, `href="/comptes"`)
	e.expectContains("/utilisateurs", "alice", `<span class="badge">Vous</span>`)

	newUser := url.Values{"username": {"bob"}, "password": {"une autre phrase solide"}, "confirm": {"différente"}}
	if res := e.do("POST", "/utilisateurs", newUser); res.status != http.StatusUnprocessableEntity || !strings.Contains(res.body, "diffèrent") || !strings.Contains(res.body, `value="bob"`) {
		t.Errorf("confirmation différente : %d", res.status)
	}
	newUser.Set("confirm", "une autre phrase solide")
	e.expectRedirect("POST", "/utilisateurs", newUser, "/utilisateurs")
	body := e.expectContains("/utilisateurs", "Compte créé", "bob")
	if res := e.do("POST", "/utilisateurs", newUser); res.status != http.StatusUnprocessableEntity || !strings.Contains(res.body, "déjà pris") {
		t.Errorf("identifiant en double : %d", res.status)
	}
	if strings.Count(body, "/supprimer") != 1 {
		t.Error("seuls les autres comptes se suppriment")
	}

	// Mot de passe : l'actuel est exigé ; la session courante reste ouverte.
	change := url.Values{"current": {"faux"}, "new_password": {"un nouveau mot de passe"}, "confirm": {"un nouveau mot de passe"}}
	if res := e.do("POST", "/utilisateurs/mot-de-passe", change); res.status != http.StatusUnprocessableEntity || !strings.Contains(res.body, "actuel incorrect") {
		t.Errorf("mot de passe actuel faux : %d", res.status)
	}
	change.Set("current", password)
	e.expectRedirect("POST", "/utilisateurs/mot-de-passe", change, "/utilisateurs")
	e.expectContains("/utilisateurs", "Mot de passe changé")

	bob := regexp.MustCompile(`/utilisateurs/(\d+)/supprimer`).FindStringSubmatch(body)
	if bob == nil {
		t.Fatal("suppression introuvable")
	}
	e.expectRedirect("POST", bob[0], nil, "/utilisateurs")
	if body := e.expectContains("/utilisateurs", "Compte supprimé"); strings.Contains(body, "bob") {
		t.Error("compte supprimé encore listé")
	}
	if res := e.do("POST", bob[0], nil); res.status != http.StatusNotFound {
		t.Errorf("compte inconnu : %d", res.status)
	}

	// Déconnecté, l'ancien mot de passe ne fonctionne plus.
	e.do("POST", "/logout", nil)
	if res := e.do("POST", "/login", url.Values{"username": {"alice"}, "password": {password}}); res.status == http.StatusSeeOther {
		t.Error("ancien mot de passe accepté")
	}
	e.expectRedirect("POST", "/login", url.Values{"username": {"alice"}, "password": {"un nouveau mot de passe"}}, "/")
}
