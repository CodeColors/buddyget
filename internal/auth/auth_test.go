package auth

import (
	"errors"
	"net/netip"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codecolors/buddyget/internal/platform/db"
)

const testPassword = "un mot de passe solide"

var testIP = netip.MustParseAddr("192.0.2.1")

// newTestService renvoie un service sur une base vierge et une horloge
// pilotable.
func newTestService(t *testing.T) (*Service, *time.Time) {
	t.Helper()
	d, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	s := NewService(d)
	s.now = func() time.Time { return now }
	return s, &now
}

func TestCreateFirstUser(t *testing.T) {
	s, _ := newTestService(t)
	ctx := t.Context()

	if need, err := s.NeedsSetup(ctx); err != nil || !need {
		t.Fatalf("NeedsSetup = %v, %v ; attendu true", need, err)
	}
	u, err := s.CreateFirstUser(ctx, "  Alice ", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	if u.Username != "Alice" || u.Theme != ThemeAuto || u.ID == 0 {
		t.Errorf("utilisateur inattendu : %+v", u)
	}
	if need, _ := s.NeedsSetup(ctx); need {
		t.Error("NeedsSetup doit être faux après la création")
	}
	if _, err := s.CreateFirstUser(ctx, "bob", testPassword); !errors.Is(err, ErrSetupDone) {
		t.Errorf("second premier compte : %v, attendu ErrSetupDone", err)
	}
}

func TestCreateFirstUserIsAtomic(t *testing.T) {
	s, _ := newTestService(t)
	var wg sync.WaitGroup
	var mu sync.Mutex
	created := 0
	for i := range 4 {
		wg.Go(func() {
			if _, err := s.CreateFirstUser(t.Context(), "user"+string(rune('a'+i)), testPassword); err == nil {
				mu.Lock()
				created++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if created != 1 {
		t.Errorf("%d comptes créés, attendu 1", created)
	}
}

func TestCreateFirstUserValidation(t *testing.T) {
	s, _ := newTestService(t)
	for name, c := range map[string]struct{ user, pw string }{
		"identifiant vide":      {"   ", testPassword},
		"identifiant trop long": {strings.Repeat("é", UsernameMaxLen+1), testPassword},
		"mot de passe court":    {"alice", strings.Repeat("x", PasswordMinLen-1)},
		"mot de passe long":     {"alice", strings.Repeat("x", PasswordMaxLen+1)},
	} {
		t.Run(name, func(t *testing.T) {
			var ie InputError
			if _, err := s.CreateFirstUser(t.Context(), c.user, c.pw); !errors.As(err, &ie) {
				t.Errorf("erreur = %v, attendu InputError", err)
			}
		})
	}
	if need, _ := s.NeedsSetup(t.Context()); !need {
		t.Error("aucun compte ne doit avoir été créé")
	}
}

func TestAuthenticate(t *testing.T) {
	s, _ := newTestService(t)
	ctx := t.Context()
	if _, err := s.CreateFirstUser(ctx, "Alice", testPassword); err != nil {
		t.Fatal(err)
	}

	u, err := s.Authenticate(ctx, testIP, "alice", testPassword)
	if err != nil || u.Username != "Alice" {
		t.Fatalf("connexion insensible à la casse refusée : %+v, %v", u, err)
	}
	for _, c := range []struct{ user, pw string }{
		{"Alice", "mauvais mot de passe"},
		{"inconnu", testPassword},
	} {
		if _, err := s.Authenticate(ctx, testIP, c.user, c.pw); !errors.Is(err, ErrInvalidCredentials) {
			t.Errorf("Authenticate(%q) = %v, attendu ErrInvalidCredentials", c.user, err)
		}
	}
}

func TestAuthenticateThrottling(t *testing.T) {
	s, now := newTestService(t)
	ctx := t.Context()
	if _, err := s.CreateFirstUser(ctx, "alice", testPassword); err != nil {
		t.Fatal(err)
	}
	for range loginMaxFails {
		if _, err := s.Authenticate(ctx, testIP, "alice", "mauvais"); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatal(err)
		}
	}

	// Même le bon mot de passe est refusé pendant le blocage, quelle que soit
	// l'adresse IP (blocage par identifiant).
	other := netip.MustParseAddr("198.51.100.7")
	var te *ThrottledError
	if _, err := s.Authenticate(ctx, other, "ALICE", testPassword); !errors.As(err, &te) || te.RetryAfter != loginWindow {
		t.Fatalf("erreur = %v, attendu ThrottledError de %v", err, loginWindow)
	}

	*now = now.Add(loginWindow)
	if _, err := s.Authenticate(ctx, testIP, "alice", testPassword); err != nil {
		t.Errorf("connexion refusée après la fenêtre : %v", err)
	}
}

func TestSessionLifecycle(t *testing.T) {
	s, now := newTestService(t)
	ctx := t.Context()
	u, err := s.CreateFirstUser(ctx, "alice", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := s.CreateSession(ctx, u)
	if err != nil {
		t.Fatal(err)
	}
	if len(sess.Token) < 26 || !sess.ExpiresAt.Equal(now.Add(sessionTTL)) {
		t.Errorf("session inattendue : %+v", sess)
	}

	got, err := s.Session(ctx, sess.Token)
	if err != nil || got.User != u || got.Renewed {
		t.Fatalf("Session = %+v, %v", got, err)
	}
	for _, token := range []string{"", "inconnu"} {
		if _, err := s.Session(ctx, token); !errors.Is(err, ErrNoSession) {
			t.Errorf("Session(%q) = %v, attendu ErrNoSession", token, err)
		}
	}

	// Au-delà de la moitié de la durée de vie, la session est prolongée.
	*now = now.Add(sessionTTL/2 + time.Hour)
	got, err = s.Session(ctx, sess.Token)
	if err != nil || !got.Renewed || !got.ExpiresAt.Equal(now.Add(sessionTTL)) {
		t.Fatalf("session non prolongée : %+v, %v", got, err)
	}

	// Sans visite pendant toute la durée de vie, elle expire.
	*now = got.ExpiresAt
	if _, err := s.Session(ctx, sess.Token); !errors.Is(err, ErrNoSession) {
		t.Errorf("session expirée acceptée : %v", err)
	}

	// Une nouvelle connexion purge les sessions expirées.
	if _, err := s.CreateSession(ctx, u); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.R.QueryRowContext(ctx, `SELECT count(*) FROM sessions`).Scan(&n); err != nil || n != 1 {
		t.Errorf("%d sessions en base, attendu 1 (%v)", n, err)
	}
}

func TestDeleteSessionAndTheme(t *testing.T) {
	s, _ := newTestService(t)
	ctx := t.Context()
	u, _ := s.CreateFirstUser(ctx, "alice", testPassword)
	sess, _ := s.CreateSession(ctx, u)

	if err := s.SetTheme(ctx, u.ID, ThemeDark); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Session(ctx, sess.Token); got.User.Theme != ThemeDark {
		t.Errorf("thème = %q, attendu dark", got.User.Theme)
	}
	if err := s.DeleteSession(ctx, sess.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Session(ctx, sess.Token); !errors.Is(err, ErrNoSession) {
		t.Errorf("session supprimée encore valide : %v", err)
	}
}

func TestParseTheme(t *testing.T) {
	for in, ok := range map[string]bool{"auto": true, "light": true, "dark": true, "": false, "Dark": false} {
		if _, got := ParseTheme(in); got != ok {
			t.Errorf("ParseTheme(%q) = %v, attendu %v", in, got, ok)
		}
	}
}
