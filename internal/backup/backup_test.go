package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/platform/db"
	"github.com/codecolors/buddyget/internal/platform/validation"
)

const passphrase = "une phrase de passe solide"

var now = time.Date(2026, 11, 1, 3, 15, 0, 0, time.Local)

type env struct {
	t       testing.TB
	dir     string
	db      *db.DB
	ledger  *ledger.Service
	s       *Service
	account int64
}

func openEnv(t testing.TB, dir, pass string) *env {
	t.Helper()
	d, err := db.Open(t.Context(), filepath.Join(dir, "buddyget.db"))
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, dir: dir, db: d, ledger: ledger.NewService(d), s: NewService(d, dir, pass)}
	t.Cleanup(func() { _ = e.db.Close() })
	return e
}

func newEnv(t testing.TB, pass string) *env {
	t.Helper()
	e := openEnv(t, t.TempDir(), pass)
	var err error
	if e.account, err = e.ledger.CreateAccount(t.Context(), ledger.AccountInput{Name: "Courant", Kind: ledger.AccountCurrent, Bank: ledger.BankSG, OpeningDate: "2026-01-01"}); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) op(label string) {
	e.t.Helper()
	if _, err := e.ledger.CreateTransaction(e.t.Context(), ledger.TransactionInput{AccountID: e.account, Date: "2026-10-31", Label: label, Amount: -10_00}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) labels() []string {
	e.t.Helper()
	page, err := e.ledger.Search(e.t.Context(), ledger.Filter{}, 0)
	if err != nil {
		e.t.Fatal(err)
	}
	var out []string
	for _, t := range page.Items {
		out = append(out, t.Label)
	}
	return out
}

func (e *env) settings(change func(*Settings)) Settings {
	e.t.Helper()
	st, err := e.s.Settings(e.t.Context())
	if err != nil {
		e.t.Fatal(err)
	}
	if change != nil {
		change(&st)
		if err := e.s.SaveSettings(e.t.Context(), st); err != nil {
			e.t.Fatal(err)
		}
		st, _ = e.s.Settings(e.t.Context())
	}
	return st
}

// TestBackupAndRestore sauvegarde une base chiffrée et ses pièces jointes,
// puis la restaure après des modifications : tout revient à l'état
// sauvegardé, les données remplacées sont mises de côté.
func TestBackupAndRestore(t *testing.T) {
	e := newEnv(t, passphrase)
	ctx := t.Context()
	e.op("Avant la sauvegarde")
	files := filepath.Join(e.dir, "attachments")
	if err := os.MkdirAll(files, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(files, "ticket.jpg"), []byte("photo"), 0o600); err != nil {
		t.Fatal(err)
	}

	a, err := e.s.Run(ctx, e.settings(nil), false, now)
	if err != nil {
		t.Fatal(err)
	}
	if a.Name != "buddyget-20261101-031500.tar.gz.age" || !a.Encrypted || a.Auto {
		t.Errorf("archive : %+v", a)
	}
	raw, _ := os.ReadFile(filepath.Join(e.dir, "backups", a.Name))
	if !bytes.HasPrefix(raw, []byte("age-encryption.org/v1")) || bytes.Contains(raw, []byte("Avant la sauvegarde")) {
		t.Error("archive non chiffrée")
	}
	if st := e.settings(nil); st.LastAt == "" || st.LastError != "" {
		t.Errorf("compte rendu : %+v", st)
	}
	if list, _ := e.s.List(); len(list) != 1 || list[0].Name != a.Name || list[0].Size != int64(len(raw)) {
		t.Errorf("liste : %+v", list)
	}

	// Modifications après la sauvegarde, puis restauration.
	e.op("Après la sauvegarde")
	if err := os.WriteFile(filepath.Join(files, "autre.jpg"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := e.s.StageArchive(ctx, a.Name); err != nil {
		t.Fatal(err)
	}
	if !e.s.Pending() {
		t.Fatal("restauration en attente")
	}
	if err := e.db.Close(); err != nil {
		t.Fatal(err)
	}
	applied, err := ApplyPending(e.dir, now)
	if err != nil || !applied {
		t.Fatalf("restauration : %v, %v", applied, err)
	}
	restored := openEnv(t, e.dir, passphrase)
	if got := restored.labels(); len(got) != 1 || got[0] != "Avant la sauvegarde" {
		t.Errorf("opérations restaurées : %v", got)
	}
	if data, err := os.ReadFile(filepath.Join(files, "ticket.jpg")); err != nil || string(data) != "photo" {
		t.Errorf("pièce jointe restaurée : %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(files, "autre.jpg")); !errors.Is(err, os.ErrNotExist) {
		t.Error("pièce jointe postérieure à la sauvegarde")
	}
	if _, err := os.Stat(filepath.Join(e.dir, previousDir, "attachments", "autre.jpg")); err != nil {
		t.Error("les données remplacées sont mises de côté")
	}
	if restored.s.Pending() {
		t.Error("restauration appliquée une seule fois")
	}
	if applied, err := ApplyPending(e.dir, now); applied || err != nil {
		t.Errorf("rien à restaurer : %v, %v", applied, err)
	}
}

func TestRestoreRefusals(t *testing.T) {
	e := newEnv(t, passphrase)
	ctx := t.Context()
	a, err := e.s.Run(ctx, e.settings(nil), false, now)
	if err != nil {
		t.Fatal(err)
	}
	f, _ := e.s.Open(a.Name)
	encrypted, _ := io.ReadAll(f)
	_ = f.Close()

	for name, c := range map[string]struct {
		pass string
		data []byte
	}{
		"sans phrase de passe":   {"", encrypted},
		"autre phrase de passe":  {"une autre phrase solide", encrypted},
		"archive tronquée":       {passphrase, encrypted[:len(encrypted)/2]},
		"pas une archive":        {passphrase, []byte("bonjour")},
		"chemin hors du dossier": {passphrase, tarball(t, map[string]string{"manifest.json": `{"application":"BuddyGet","format":1}`, "../evade.db": "x"})},
		"sans base":              {passphrase, tarball(t, map[string]string{"manifest.json": `{"application":"BuddyGet","format":1}`})},
		"autre application":      {passphrase, tarball(t, map[string]string{"manifest.json": `{"application":"Autre","format":1}`, "buddyget.db": "x"})},
		"base illisible":         {passphrase, tarball(t, map[string]string{"manifest.json": `{"application":"BuddyGet","format":1}`, "buddyget.db": "pas une base"})},
		"format plus récent":     {passphrase, tarball(t, map[string]string{"manifest.json": `{"application":"BuddyGet","format":99}`, "buddyget.db": "x"})},
		"sous-dossier inattendu": {passphrase, tarball(t, map[string]string{"manifest.json": `{"application":"BuddyGet","format":1}`, "attachments/a/b.jpg": "x"})},
	} {
		s := NewService(e.db, e.dir, c.pass)
		if err := s.Stage(ctx, bytes.NewReader(c.data)); !isInput(err) {
			t.Errorf("%s : %v", name, err)
		}
		if s.Pending() {
			t.Errorf("%s : rien ne doit attendre le redémarrage", name)
		}
	}

	// Une base d'une version plus récente de BuddyGet est refusée.
	newer := filepath.Join(t.TempDir(), "newer.db")
	if err := e.db.Snapshot(ctx, newer); err != nil {
		t.Fatal(err)
	}
	conn, err := db.Open(ctx, newer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.W.ExecContext(ctx, `INSERT INTO schema_migrations (version) VALUES (999)`); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	data, _ := os.ReadFile(newer)
	if err := e.s.Stage(ctx, bytes.NewReader(tarball(t, map[string]string{"manifest.json": `{"application":"BuddyGet","format":1}`, "buddyget.db": string(data)}))); !isInput(err) || !strings.Contains(err.Error(), "plus récente") {
		t.Errorf("base plus récente : %v", err)
	}
	if _, err := e.s.Open("../buddyget.db"); !errors.Is(err, ErrNotFound) {
		t.Errorf("nom d'archive arbitraire : %v", err)
	}
}

// tarball fabrique une archive non chiffrée.
func tarball(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(content))}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write([]byte(content))
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func TestUnencryptedRotationAndCopies(t *testing.T) {
	e := newEnv(t, "")
	ctx := t.Context()
	copyDir := t.TempDir()
	st := e.settings(func(st *Settings) { st.Auto, st.Keep, st.Copy, st.CopyDir = true, 2, FolderCopy, copyDir })

	manual, err := e.s.Run(ctx, st, false, now)
	if err != nil {
		t.Fatal(err)
	}
	if manual.Encrypted || manual.Name != "buddyget-20261101-031500.tar.gz" {
		t.Errorf("archive non chiffrée : %+v", manual)
	}
	for i := range 3 {
		if err := e.s.Daily(ctx, now.Add(time.Duration(i+1)*24*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	names := func(dir string) []string {
		list, err := list(dir)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, a := range list {
			out = append(out, a.Name)
		}
		return out
	}
	want := "buddyget-auto-20261104-031500.tar.gz buddyget-auto-20261103-031500.tar.gz buddyget-20261101-031500.tar.gz"
	if got := strings.Join(names(e.s.dir), " "); got != want {
		t.Errorf("rotation : %s", got)
	}
	if got := strings.Join(names(copyDir), " "); got != want {
		t.Errorf("copie dans le dossier : %s", got)
	}

	// Restauration d'une archive non chiffrée.
	e.op("Après")
	if err := e.s.StageArchive(ctx, manual.Name); err != nil {
		t.Fatal(err)
	}

	// Copie WebDAV : dépôt authentifié ; un refus est consigné.
	var mu sync.Mutex
	received := map[string]int{}
	dav := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, _ := r.BasicAuth()
		if r.Method != http.MethodPut || user != "hugo" || pass != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		received[r.URL.Path] = len(body)
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(dav.Close)
	st = e.settings(func(st *Settings) {
		st.Copy, st.WebDAVURL, st.WebDAVUser, st.WebDAVPassword = WebDAVCopy, dav.URL+"/sauvegardes/", "hugo", "secret"
	})
	a, err := e.s.Run(ctx, st, true, now.Add(5*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if received["/sauvegardes/"+a.Name] != int(a.Size) {
		t.Errorf("dépôt WebDAV : %v", received)
	}
	// Mot de passe laissé vide : le précédent est conservé.
	st = e.settings(func(st *Settings) { st.WebDAVPassword = "" })
	if st.WebDAVPassword != "secret" {
		t.Error("mot de passe WebDAV conservé")
	}
	st = e.settings(func(st *Settings) { st.WebDAVUser = "autre" })
	if _, err := e.s.Run(ctx, st, true, now.Add(6*24*time.Hour)); err == nil {
		t.Error("refus WebDAV ignoré")
	}
	if st := e.settings(nil); !strings.Contains(st.LastError, "WebDAV") {
		t.Errorf("échec consigné : %+v", st)
	}
}

func TestSettingsValidation(t *testing.T) {
	e := newEnv(t, "")
	base := e.settings(nil)
	if base.Auto || base.Keep != 7 || base.Copy != NoCopy {
		t.Errorf("réglages par défaut : %+v", base)
	}
	for _, change := range []func(*Settings){
		func(st *Settings) { st.Keep = 0 },
		func(st *Settings) { st.Copy = "ftp" },
		func(st *Settings) { st.Copy, st.CopyDir = FolderCopy, "relatif" },
		func(st *Settings) { st.Copy, st.CopyDir = FolderCopy, "/n/existe/pas" },
		func(st *Settings) { st.Copy, st.CopyDir = FolderCopy, filepath.Join(e.dir, "backups") },
		func(st *Settings) { st.Copy, st.WebDAVURL = WebDAVCopy, "ftp://nas" },
	} {
		st := base
		change(&st)
		if err := os.MkdirAll(filepath.Join(e.dir, "backups"), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := e.s.SaveSettings(t.Context(), st); !isInput(err) {
			t.Errorf("réglage invalide accepté : %+v (%v)", st, err)
		}
	}
	if err := e.s.Delete("buddyget-20261101-031500.tar.gz"); !errors.Is(err, ErrNotFound) {
		t.Errorf("archive inconnue : %v", err)
	}
}

func isInput(err error) bool {
	_, ok := validation.Message(err)
	return ok
}
