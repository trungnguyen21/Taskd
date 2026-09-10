package store

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"io"
	"time"

	"github.com/jackc/pgx/v4/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

// SessionLifetime is how long a login lasts.
const SessionLifetime = 30 * 24 * time.Hour

// UserStore handles the owner's password and sessions.
type UserStore struct {
	pool *pgxpool.Pool
}

func NewUserStore(pool *pgxpool.Pool) *UserStore {
	return &UserStore{pool: pool}
}

// EnsurePassword sets the owner's password if none is stored yet. It runs on
// startup so that an installation is closed from its first boot rather than
// after a setup step the operator might not reach.
func (s *UserStore) EnsurePassword(ctx context.Context, userID, password string) error {
	var existing string
	err := s.pool.QueryRow(ctx,
		`SELECT password_hash FROM users WHERE id = $1`, userID).Scan(&existing)
	if err != nil {
		return translateNoRows(err)
	}
	if existing != "" {
		return nil
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx,
		`UPDATE users SET password_hash = $2 WHERE id = $1`, userID, string(hash))
	return err
}

// HasPassword reports whether a password has been set.
func (s *UserStore) HasPassword(ctx context.Context, userID string) (bool, error) {
	var hash string
	if err := s.pool.QueryRow(ctx,
		`SELECT password_hash FROM users WHERE id = $1`, userID).Scan(&hash); err != nil {
		return false, translateNoRows(err)
	}
	return hash != "", nil
}

// CheckPassword reports whether the password is the stored one.
func (s *UserStore) CheckPassword(ctx context.Context, userID, password string) (bool, error) {
	var hash string
	if err := s.pool.QueryRow(ctx,
		`SELECT password_hash FROM users WHERE id = $1`, userID).Scan(&hash); err != nil {
		return false, translateNoRows(err)
	}
	if hash == "" {
		return false, nil
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil, nil
}

// CreateSession issues a session and returns its token.
func (s *UserStore) CreateSession(ctx context.Context, userID string, now time.Time) (string, error) {
	raw := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)

	if _, err := s.pool.Exec(ctx,
		`INSERT INTO sessions (id, user_id, expires_at) VALUES ($1,$2,$3)`,
		token, userID, now.Add(SessionLifetime)); err != nil {
		return "", err
	}
	return token, nil
}

// UserForSession returns the user a live session belongs to.
func (s *UserStore) UserForSession(ctx context.Context, token string, now time.Time) (string, error) {
	var userID string
	err := s.pool.QueryRow(ctx,
		`SELECT user_id FROM sessions WHERE id = $1 AND expires_at > $2`, token, now).
		Scan(&userID)
	if err != nil {
		return "", translateNoRows(err)
	}
	return userID, nil
}

// DeleteSession ends a session.
func (s *UserStore) DeleteSession(ctx context.Context, token string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, token)
	return err
}
