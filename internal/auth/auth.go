// Package auth gère les comptes utilisateurs locaux : création du premier
// compte, vérification des mots de passe (argon2id), limitation des
// tentatives, sessions et préférences d'affichage.
package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/codecolors/buddyget/internal/platform/db"
)

// Limites de saisie des identifiants.
const (
	UsernameMaxLen = 64
	PasswordMinLen = 12
	PasswordMaxLen = 256
)

// Anti-bruteforce : au-delà de loginMaxFails échecs pour une même adresse IP
// ou un même identifiant, les tentatives sont refusées jusqu'à la fin de la
// fenêtre.
const (
	loginMaxFails     = 5
	loginWindow       = 15 * time.Minute
	limiterMaxEntries = 10_000
)

var (
	ErrInvalidCredentials = errors.New("identifiant ou mot de passe incorrect")
	ErrSetupDone          = errors.New("le premier compte existe déjà")
)

// InputError signale une saisie invalide ; son message s'affiche tel quel.
type InputError string

func (e InputError) Error() string { return string(e) }

// ThrottledError indique que les tentatives de connexion sont suspendues.
type ThrottledError struct{ RetryAfter time.Duration }

func (e *ThrottledError) Error() string {
	return fmt.Sprintf("trop de tentatives de connexion, réessayer dans %s", e.RetryAfter)
}

// Theme est la préférence d'affichage d'un utilisateur.
type Theme string

const (
	ThemeAuto  Theme = "auto" // suit prefers-color-scheme
	ThemeLight Theme = "light"
	ThemeDark  Theme = "dark"
)

// ParseTheme valide une valeur de thème issue d'un formulaire.
func ParseTheme(s string) (Theme, bool) {
	switch t := Theme(s); t {
	case ThemeAuto, ThemeLight, ThemeDark:
		return t, true
	}
	return "", false
}

// User est un utilisateur authentifié.
type User struct {
	ID       int64
	Username string
	Theme    Theme
}

// Service porte toute la logique d'authentification.
type Service struct {
	db        *db.DB
	now       func() time.Time
	limiter   *limiter
	setupDone atomic.Bool
	// dummyHash sert à vérifier un mot de passe même pour un identifiant
	// inconnu, pour que la durée de réponse ne trahisse pas son existence.
	dummyHash func() (string, error)
}

// NewService crée le service d'authentification.
func NewService(d *db.DB) *Service {
	return &Service{
		db:      d,
		now:     time.Now,
		limiter: newLimiter(loginMaxFails, loginWindow, limiterMaxEntries),
		dummyHash: sync.OnceValues(func() (string, error) {
			return hashPassword(context.Background(), "buddyget-identifiant-inconnu")
		}),
	}
}

// NeedsSetup indique qu'aucun compte n'existe encore. Une fois un compte
// créé, la réponse est mémorisée et ne coûte plus de requête.
func (s *Service) NeedsSetup(ctx context.Context) (bool, error) {
	if s.setupDone.Load() {
		return false, nil
	}
	var exists bool
	if err := s.db.R.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM users)`).Scan(&exists); err != nil {
		return false, err
	}
	s.setupDone.Store(exists)
	return !exists, nil
}

// CreateFirstUser crée le premier compte. L'insertion est conditionnelle :
// deux requêtes simultanées ne peuvent pas créer deux « premiers » comptes.
func (s *Service) CreateFirstUser(ctx context.Context, username, password string) (User, error) {
	username, err := validateCredentials(username, password)
	if err != nil {
		return User{}, err
	}
	hash, err := hashPassword(ctx, password)
	if err != nil {
		return User{}, err
	}
	res, err := s.db.W.ExecContext(ctx,
		`INSERT INTO users (username, password_hash) SELECT ?, ? WHERE NOT EXISTS (SELECT 1 FROM users)`,
		username, hash)
	if err != nil {
		return User{}, err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return User{}, errors.Join(ErrSetupDone, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return User{}, err
	}
	s.setupDone.Store(true)
	return User{ID: id, Username: username, Theme: ThemeAuto}, nil
}

// Authenticate vérifie un couple identifiant / mot de passe. Les échecs
// sont comptés par adresse IP et par identifiant.
func (s *Service) Authenticate(ctx context.Context, ip netip.Addr, username, password string) (User, error) {
	username = strings.TrimSpace(username)
	keys := []string{"ip:" + ip.String(), "user:" + strings.ToLower(username)}
	now := s.now()
	if wait := s.limiter.retryAfter(now, keys...); wait > 0 {
		return User{}, &ThrottledError{RetryAfter: wait}
	}

	var u User
	var hash string
	err := s.db.R.QueryRowContext(ctx,
		`SELECT id, username, theme, password_hash FROM users WHERE username = ?`, username,
	).Scan(&u.ID, &u.Username, &u.Theme, &hash)
	found := err == nil
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if hash, err = s.dummyHash(); err != nil {
			return User{}, err
		}
	case err != nil:
		return User{}, err
	}

	ok, err := verifyPassword(ctx, password, hash)
	if err != nil {
		return User{}, err
	}
	if !ok || !found {
		s.limiter.fail(now, keys...)
		return User{}, ErrInvalidCredentials
	}
	s.limiter.reset(keys...)
	return u, nil
}

// SetTheme enregistre la préférence d'affichage de l'utilisateur.
func (s *Service) SetTheme(ctx context.Context, userID int64, t Theme) error {
	_, err := s.db.W.ExecContext(ctx, `UPDATE users SET theme = ? WHERE id = ?`, t, userID)
	return err
}

// validateCredentials contrôle la saisie et renvoie l'identifiant nettoyé.
func validateCredentials(username, password string) (string, error) {
	username = strings.TrimSpace(username)
	switch n := utf8.RuneCountInString(username); {
	case n == 0:
		return "", InputError("Choisissez un identifiant.")
	case n > UsernameMaxLen:
		return "", InputError(fmt.Sprintf("L'identifiant ne doit pas dépasser %d caractères.", UsernameMaxLen))
	}
	switch n := utf8.RuneCountInString(password); {
	case n < PasswordMinLen:
		return "", InputError(fmt.Sprintf("Le mot de passe doit contenir au moins %d caractères.", PasswordMinLen))
	case n > PasswordMaxLen:
		return "", InputError(fmt.Sprintf("Le mot de passe ne doit pas dépasser %d caractères.", PasswordMaxLen))
	}
	return username, nil
}
