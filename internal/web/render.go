package web

import (
	"bytes"
	"compress/gzip"
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
	Flash string // confirmation de l'action précédente
	Data  any    // donnée propre à la page
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

// render exécute un bloc de la page (« layout » pour la page entière) dans
// un tampon avant d'écrire la réponse : une erreur de gabarit ne produit
// jamais de page tronquée.
func (r *renderer) render(w http.ResponseWriter, status int, page, block string, v view) error {
	t, ok := r.pages[page]
	if !ok {
		return fmt.Errorf("page %q inconnue", page)
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, block, v); err != nil {
		return fmt.Errorf("page %q : %w", page, err)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, err := buf.WriteTo(w)
	return err
}

// asset est un fichier statique gardé en mémoire avec son empreinte et,
// quand c'est rentable, sa version compressée (calculée une seule fois).
type asset struct {
	body        []byte
	gzipped     []byte
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
		gz, err := gzipBytes(body)
		if err != nil {
			return nil, err
		}
		a[e.Name()] = asset{body: body, gzipped: gz, contentType: contentType(e.Name()), version: hex.EncodeToString(sum[:6])}
	}
	return a, nil
}

// gzipBytes compresse un contenu ; nil s'il n'y gagne pas au moins 10 %.
func gzipBytes(body []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := zw.Write(body); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	if buf.Len() > len(body)*9/10 {
		return nil, nil
	}
	return buf.Bytes(), nil
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
	body, etag := f.body, f.version
	if f.gzipped != nil {
		h.Set("Vary", "Accept-Encoding")
		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			body, etag = f.gzipped, f.version+"-gz"
			h.Set("Content-Encoding", "gzip")
		}
	}
	h.Set("ETag", `"`+etag+`"`)
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(body))
}
