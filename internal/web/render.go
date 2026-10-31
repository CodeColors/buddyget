package web

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"html/template"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/codecolors/buddyget/internal/auth"
)

var (
	//go:embed templates
	templateFS embed.FS
	//go:embed static
	staticFS embed.FS
)

// view est la donnée commune passée au gabarit de mise en page.
type view struct {
	Title string
	Path  string // chemin courant : lien actif, retour après changement de thème
	Theme auth.Theme
	User  *auth.User
	Data  any // donnée propre à la page
}

// renderer associe chaque page (templates/pages/<nom>.html) à un jeu de
// gabarits analysé une fois pour toutes au démarrage.
type renderer struct {
	pages map[string]*template.Template
}

func newRenderer(funcs template.FuncMap) (*renderer, error) {
	layout, err := template.New("").Funcs(funcs).ParseFS(templateFS, "templates/layout.html")
	if err != nil {
		return nil, err
	}
	files, err := fs.Glob(templateFS, "templates/pages/*.html")
	if err != nil {
		return nil, err
	}
	r := &renderer{pages: make(map[string]*template.Template, len(files))}
	for _, f := range files {
		t, err := layout.Clone()
		if err == nil {
			t, err = t.ParseFS(templateFS, f)
		}
		if err != nil {
			return nil, err
		}
		r.pages[strings.TrimSuffix(path.Base(f), ".html")] = t
	}
	return r, nil
}

// render exécute la page dans un tampon avant d'écrire la réponse : une
// erreur de gabarit ne produit jamais de page tronquée.
func (r *renderer) render(w http.ResponseWriter, status int, page string, v view) error {
	t, ok := r.pages[page]
	if !ok {
		return fmt.Errorf("page %q inconnue", page)
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "layout", v); err != nil {
		return fmt.Errorf("page %q : %w", page, err)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, err := buf.WriteTo(w)
	return err
}

// asset est un fichier statique gardé en mémoire avec son empreinte.
type asset struct {
	body        []byte
	contentType string
	version     string
}

// assets sert les fichiers de static/. Les URL produites par url portent
// l'empreinte du contenu (?v=…) et sont mises en cache sans limite par le
// navigateur ; une nouvelle version change l'URL.
type assets map[string]asset

func loadAssets() (assets, error) {
	entries, err := fs.ReadDir(staticFS, "static")
	if err != nil {
		return nil, err
	}
	a := make(assets, len(entries))
	for _, e := range entries {
		body, err := fs.ReadFile(staticFS, "static/"+e.Name())
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(body)
		a[e.Name()] = asset{body: body, contentType: contentType(e.Name()), version: hex.EncodeToString(sum[:6])}
	}
	return a, nil
}

func contentType(name string) string {
	if path.Ext(name) == ".webmanifest" {
		return "application/manifest+json"
	}
	if t := mime.TypeByExtension(path.Ext(name)); t != "" {
		return t
	}
	return "application/octet-stream"
}

// url renvoie l'adresse versionnée d'un fichier statique.
func (a assets) url(name string) (string, error) {
	f, ok := a[name]
	if !ok {
		return "", fmt.Errorf("fichier statique %q inconnu", name)
	}
	return "/static/" + name + "?v=" + f.version, nil
}

func (a assets) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f, ok := a[r.PathValue("file")]
	if !ok {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	if r.URL.Query().Get("v") == f.version {
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		h.Set("Cache-Control", "no-cache")
	}
	h.Set("Content-Type", f.contentType)
	h.Set("ETag", `"`+f.version+`"`)
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(f.body))
}
