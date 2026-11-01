package auth

import (
	"errors"
	"net/netip"
	"testing"
)

func TestUsers(t *testing.T) {
	s, _ := newTestService(t)
	ctx := t.Context()
	first, err := s.CreateFirstUser(ctx, "hugo", "une phrase de passe solide")
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][2]string{{"", "une phrase de passe solide"}, {"lea", "court"}, {"HUGO", "une phrase de passe solide"}} {
		if _, err := s.CreateUser(ctx, bad[0], bad[1]); !isInputError(err) {
			t.Errorf("compte invalide accepté : %q (%v)", bad[0], err)
		}
	}
	lea, err := s.CreateUser(ctx, " lea ", "une autre phrase solide")
	if err != nil {
		t.Fatal(err)
	}
	if users, _ := s.Users(ctx); len(users) != 2 || users[0].Username != "hugo" || users[1].Username != "lea" {
		t.Errorf("utilisateurs : %+v", users)
	}
	ip := netip.MustParseAddr("192.0.2.1")
	if _, err := s.Authenticate(ctx, ip, "lea", "une autre phrase solide"); err != nil {
		t.Errorf("connexion du nouveau compte : %v", err)
	}

	// Changement de mot de passe : l'actuel est exigé, les autres sessions
	// sont fermées.
	kept, _ := s.CreateSession(ctx, first)
	other, _ := s.CreateSession(ctx, first)
	if err := s.ChangePassword(ctx, first.ID, "mauvais", "un nouveau mot de passe", kept.Token); !isInputError(err) {
		t.Errorf("mot de passe actuel faux : %v", err)
	}
	if err := s.ChangePassword(ctx, first.ID, "une phrase de passe solide", "court", kept.Token); !isInputError(err) {
		t.Errorf("nouveau mot de passe trop court : %v", err)
	}
	if err := s.ChangePassword(ctx, first.ID, "une phrase de passe solide", "un nouveau mot de passe", kept.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Session(ctx, kept.Token); err != nil {
		t.Errorf("session conservée : %v", err)
	}
	if _, err := s.Session(ctx, other.Token); !errors.Is(err, ErrNoSession) {
		t.Errorf("autre session fermée : %v", err)
	}
	if _, err := s.Authenticate(ctx, ip, "hugo", "un nouveau mot de passe"); err != nil {
		t.Errorf("nouveau mot de passe : %v", err)
	}

	// Suppression : pas son propre compte.
	if err := s.DeleteUser(ctx, first.ID, first.ID); !isInputError(err) {
		t.Errorf("suppression de son propre compte : %v", err)
	}
	if err := s.DeleteUser(ctx, lea, first.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteUser(ctx, lea, first.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("compte supprimé : %v", err)
	}
	if err := s.ChangePassword(ctx, lea, "x", "y", ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("compte inconnu : %v", err)
	}

	// Mot de passe oublié : nouveau mot de passe aléatoire, sessions fermées.
	reset, err := s.ResetPassword(ctx, "HUGO")
	if err != nil || len(reset) < PasswordMinLen {
		t.Fatalf("réinitialisation : %q, %v", reset, err)
	}
	if _, err := s.Session(ctx, kept.Token); !errors.Is(err, ErrNoSession) {
		t.Errorf("sessions fermées : %v", err)
	}
	if _, err := s.Authenticate(ctx, ip, "hugo", reset); err != nil {
		t.Errorf("connexion avec le mot de passe réinitialisé : %v", err)
	}
	if _, err := s.ResetPassword(ctx, "inconnu"); !errors.Is(err, ErrNotFound) {
		t.Errorf("identifiant inconnu : %v", err)
	}
}
