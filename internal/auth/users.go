package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"strings"

	"github.com/codecolors/buddyget/internal/platform/db"
	"github.com/codecolors/buddyget/internal/platform/validation"
)

// ErrNotFound signale un utilisateur inexistant.
var ErrNotFound = errors.New("utilisateur introuvable")

// Users renvoie les comptes de connexion, par identifiant.
func (s *Service) Users(ctx context.Context) ([]User, error) {
	return db.Collect(ctx, s.db.R, func(row db.Scanner) (User, error) {
		var u User
		err := row.Scan(&u.ID, &u.Username, &u.Theme)
		return u, err
	}, `SELECT id, username, theme FROM users ORDER BY username COLLATE NOCASE`)
}

// CreateUser ajoute un compte de connexion ; tous les comptes ont les mêmes
// droits sur les données du foyer.
func (s *Service) CreateUser(ctx context.Context, username, password string) (int64, error) {
	username, err := validateCredentials(username, password)
	if err != nil {
		return 0, err
	}
	hash, err := hashPassword(ctx, password)
	if err != nil {
		return 0, err
	}
	res, err := s.db.W.ExecContext(ctx, `INSERT INTO users (username, password_hash) VALUES (?, ?)`, username, hash)
	if db.IsUniqueViolation(err) {
		return 0, validation.Error("Cet identifiant est déjà pris.")
	}
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ChangePassword remplace le mot de passe d'un utilisateur, après
// vérification de l'actuel ; ses autres sessions sont fermées, celle du
// jeton keep est conservée.
func (s *Service) ChangePassword(ctx context.Context, userID int64, current, next, keep string) error {
	var hash string
	if err := s.db.R.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE id = ?`, userID).Scan(&hash); err != nil {
		return errors.Join(ErrNotFound, err)
	}
	ok, err := verifyPassword(ctx, current, hash)
	if err != nil {
		return err
	}
	if !ok {
		return validation.Error("Mot de passe actuel incorrect.")
	}
	if _, err := validateCredentials("-", next); err != nil {
		return err
	}
	if hash, err = hashPassword(ctx, next); err != nil {
		return err
	}
	if _, err := s.db.W.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE id = ?`, hash, userID); err != nil {
		return err
	}
	_, err = s.db.W.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ? AND token_hash <> ?`, userID, tokenHash(keep))
	return err
}

// DeleteUser supprime un compte de connexion et ses sessions. On ne
// supprime pas son propre compte : il en reste donc toujours un.
func (s *Service) DeleteUser(ctx context.Context, id, by int64) error {
	if id == by {
		return validation.Error("Vous ne pouvez pas supprimer votre propre compte.")
	}
	res, err := s.db.W.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return errors.Join(err, ErrNotFound)
	}
	return nil
}

// ResetPassword attribue un mot de passe aléatoire à un utilisateur et ferme
// ses sessions : le recours en cas d'oubli, depuis la ligne de commande du
// NAS. Le nouveau mot de passe est renvoyé pour être communiqué.
func (s *Service) ResetPassword(ctx context.Context, username string) (string, error) {
	password := rand.Text()
	hash, err := hashPassword(ctx, password)
	if err != nil {
		return "", err
	}
	res, err := s.db.W.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE username = ?`, hash, strings.TrimSpace(username))
	if err != nil {
		return "", err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return "", errors.Join(err, ErrNotFound)
	}
	_, err = s.db.W.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = (SELECT id FROM users WHERE username = ?)`, strings.TrimSpace(username))
	return password, err
}
