package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v4/pgxpool"
)

// TelegramTokenSecret is the reserved credential name holding the bot token.
// It is a credential, so it lives sealed with the others rather than in a
// column of its own.
const TelegramTokenSecret = "telegram_bot_token"

// Settings is the per-user configuration the dashboard edits.
type Settings struct {
	TelegramChatID       string `json:"telegram_chat_id"`
	TelegramConfigured   bool   `json:"telegram_configured"`
	FailureAlertsEnabled bool   `json:"failure_alerts_enabled"`
}

// SettingsStore reads and writes per-user configuration.
type SettingsStore struct {
	pool    *pgxpool.Pool
	secrets *SecretStore
}

func NewSettingsStore(pool *pgxpool.Pool, secrets *SecretStore) *SettingsStore {
	return &SettingsStore{pool: pool, secrets: secrets}
}

// Get returns the user's settings. Whether Telegram is usable depends on both
// halves being present, so it is reported rather than left to the caller.
func (s *SettingsStore) Get(ctx context.Context, userID string) (*Settings, error) {
	var settings Settings
	err := s.pool.QueryRow(ctx,
		`SELECT telegram_chat_id, failure_alerts_enabled FROM users WHERE id = $1`, userID).
		Scan(&settings.TelegramChatID, &settings.FailureAlertsEnabled)
	if err != nil {
		return nil, translateNoRows(err)
	}

	token, err := s.TelegramToken(ctx, userID)
	if err != nil {
		return nil, err
	}
	settings.TelegramConfigured = token != "" && settings.TelegramChatID != ""

	return &settings, nil
}

// Update replaces the user's settings.
func (s *SettingsStore) Update(ctx context.Context, userID string, settings *Settings) (*Settings, error) {
	_, err := s.pool.Exec(ctx,
		`UPDATE users SET telegram_chat_id = $2, failure_alerts_enabled = $3 WHERE id = $1`,
		userID, settings.TelegramChatID, settings.FailureAlertsEnabled)
	if err != nil {
		return nil, translateNoRows(err)
	}
	return s.Get(ctx, userID)
}

// TelegramToken returns the stored bot token, or an empty string if none is
// stored. A missing credential is a configuration state, not an error.
func (s *SettingsStore) TelegramToken(ctx context.Context, userID string) (string, error) {
	if s.secrets == nil {
		return "", nil
	}
	token, err := s.secrets.Reveal(ctx, userID, TelegramTokenSecret)
	if errors.Is(err, ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return token, nil
}

// TelegramDelivery returns everything needed to send a message, and whether it
// is complete.
func (s *SettingsStore) TelegramDelivery(ctx context.Context, userID string) (token, chatID string, ok bool) {
	settings, err := s.Get(ctx, userID)
	if err != nil {
		return "", "", false
	}
	token, err = s.TelegramToken(ctx, userID)
	if err != nil {
		return "", "", false
	}
	if token == "" || settings.TelegramChatID == "" {
		return "", "", false
	}
	return token, settings.TelegramChatID, true
}
