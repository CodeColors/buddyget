package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"errors"
	"time"
)

// sessionTTL est la durée de vie d'une session sans activité ; elle est
// prolongée lors d'une visite dès qu'il en reste moins de la moitié, ce qui
// évite une écriture en base à chaque requête.
const sessionTTL = 30 * 24 * time.Hour

// ErrNoSession signale un jeton absent, inconnu ou expiré.
var ErrNoSession = errors.New("session absente ou expirée")

// Session associe un jeton opaque à un utilisateur.
type Session struct {
	Token     string
	User      User
	ExpiresAt time.Time
	// Renewed indique que l'échéance a été prolongée : le cookie doit être
	// réémis avec la nouvelle date.
	Renewed bool
}

// CreateSession ouvre une session pour l'utilisateur et purge au passage
// les sessions expirées (aucune tâche de fond n'est nécessaire).
func (s *Service) CreateSession(ctx context.Context, u User) (Session, error) {
	now := s.now()
	if _, err := s.db.W.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, now.Unix()); err != nil {
		return Session{}, err
	}
	sess := Session{Token: rand.Text(), User: u, ExpiresAt: now.Add(sessionTTL).Truncate(time.Second)}
	_, err := s.db.W.ExecContext(ctx,
		`INSERT INTO sessions (token_hash, user_id, expires_at) VALUES (?, ?, ?)`,
		tokenHash(sess.Token), u.ID, sess.ExpiresAt.Unix())
	return sess, err
}

// Session retrouve la session associée au jeton et la prolonge si besoin.
func (s *Service) Session(ctx context.Context, token string) (Session, error) {
	if token == "" {
		return Session{}, ErrNoSession
	}
	now := s.now()
	hash := tokenHash(token)
	sess := Session{Token: token}
	var expires int64
	err := s.db.R.QueryRowContext(ctx, `
		SELECT u.id, u.username, u.theme, s.expires_at
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = ? AND s.expires_at > ?`, hash, now.Unix(),
	).Scan(&sess.User.ID, &sess.User.Username, &sess.User.Theme, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNoSession
	}
	if err != nil {
		return Session{}, err
	}
	sess.ExpiresAt = time.Unix(expires, 0)

	if sess.ExpiresAt.Sub(now) < sessionTTL/2 {
		sess.ExpiresAt = now.Add(sessionTTL).Truncate(time.Second)
		sess.Renewed = true
		if _, err := s.db.W.ExecContext(ctx,
			`UPDATE sessions SET expires_at = ? WHERE token_hash = ?`, sess.ExpiresAt.Unix(), hash,
		); err != nil {
			return Session{}, err
		}
	}
	return sess, nil
}

// DeleteSession ferme la session (déconnexion).
func (s *Service) DeleteSession(ctx context.Context, token string) error {
	_, err := s.db.W.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash(token))
	return err
}

func tokenHash(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}
