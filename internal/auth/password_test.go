package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestHashAndVerifyPassword(t *testing.T) {
	ctx := t.Context()
	hash, err := hashPassword(ctx, "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Errorf("format PHC inattendu : %s", hash)
	}
	if other, _ := hashPassword(ctx, "correct horse battery staple"); other == hash {
		t.Error("deux empreintes du même mot de passe doivent différer (sel aléatoire)")
	}

	for pw, want := range map[string]bool{
		"correct horse battery staple":  true,
		"correct horse battery staple ": false,
		"":                              false,
	} {
		ok, err := verifyPassword(ctx, pw, hash)
		if err != nil || ok != want {
			t.Errorf("verifyPassword(%q) = %v, %v ; attendu %v", pw, ok, err, want)
		}
	}
}

func TestVerifyPasswordRejectsMalformedHash(t *testing.T) {
	for _, h := range []string{
		"",
		"$2a$10$bcrypt",
		"$argon2id$v=18$m=19456,t=2,p=1$c2Fs$aGFzaA",
		"$argon2id$v=19$m=x,t=2,p=1$c2Fs$aGFzaA",
		"$argon2id$v=19$m=19456,t=2,p=1$***$aGFzaA",
		"$argon2id$v=19$m=19456,t=2,p=0$c2Fs$aGFzaA",
		"$argon2id$v=19$m=19456,t=2,p=1$c2Fs$aGFzaA", // empreinte tronquée
	} {
		if _, err := verifyPassword(t.Context(), "x", h); err == nil {
			t.Errorf("empreinte %q acceptée", h)
		}
	}
}

func TestDeriveKeyHonoursContext(t *testing.T) {
	// Occupe tous les emplacements : le calcul doit attendre puis abandonner.
	for range cap(hashSlots) {
		hashSlots <- struct{}{}
	}
	defer func() {
		for range cap(hashSlots) {
			<-hashSlots
		}
	}()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := hashPassword(ctx, "peu importe"); !errors.Is(err, context.Canceled) {
		t.Errorf("erreur = %v, attendu context.Canceled", err)
	}
}
