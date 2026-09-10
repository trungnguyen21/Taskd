package store

import (
	"context"
	"time"

	"github.com/JyotinderSingh/task-queue/pkg/secretbox"
	"github.com/jackc/pgx/v4/pgxpool"
)

// Secret is a stored credential as the dashboard sees it: which key is stored,
// never the key itself.
type Secret struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	MaskedSuffix string    `json:"masked_suffix"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// SecretStore holds credentials sealed at rest.
type SecretStore struct {
	pool   *pgxpool.Pool
	sealer *secretbox.Sealer
}

func NewSecretStore(pool *pgxpool.Pool, sealer *secretbox.Sealer) *SecretStore {
	return &SecretStore{pool: pool, sealer: sealer}
}

// Put stores or replaces a credential under a name.
func (s *SecretStore) Put(ctx context.Context, userID, name, value string) (*Secret, error) {
	sealedValue, sealedDataKey, err := s.sealer.Seal(value)
	if err != nil {
		return nil, err
	}

	var secret Secret
	err = s.pool.QueryRow(ctx, `INSERT INTO secrets
		(user_id, name, encrypted_value, encrypted_data_key, masked_suffix)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (user_id, name) DO UPDATE SET
			encrypted_value = EXCLUDED.encrypted_value,
			encrypted_data_key = EXCLUDED.encrypted_data_key,
			masked_suffix = EXCLUDED.masked_suffix,
			updated_at = NOW()
		RETURNING id, name, masked_suffix, created_at, updated_at`,
		userID, name, sealedValue, sealedDataKey, secretbox.Mask(value)).
		Scan(&secret.ID, &secret.Name, &secret.MaskedSuffix, &secret.CreatedAt, &secret.UpdatedAt)
	if err != nil {
		return nil, translateNoRows(err)
	}
	return &secret, nil
}

// List returns the stored credentials without their values.
func (s *SecretStore) List(ctx context.Context, userID string) ([]*Secret, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, name, masked_suffix, created_at, updated_at
		FROM secrets WHERE user_id = $1 ORDER BY name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	secrets := []*Secret{}
	for rows.Next() {
		var secret Secret
		if err := rows.Scan(&secret.ID, &secret.Name, &secret.MaskedSuffix,
			&secret.CreatedAt, &secret.UpdatedAt); err != nil {
			return nil, err
		}
		secrets = append(secrets, &secret)
	}
	return secrets, rows.Err()
}

// Reveal returns a credential's value. It is used by workers at the start of a
// run and is never reachable through the API.
func (s *SecretStore) Reveal(ctx context.Context, userID, name string) (string, error) {
	var sealedValue, sealedDataKey []byte
	err := s.pool.QueryRow(ctx,
		`SELECT encrypted_value, encrypted_data_key FROM secrets
		WHERE user_id = $1 AND name = $2`, userID, name).
		Scan(&sealedValue, &sealedDataKey)
	if err != nil {
		return "", translateNoRows(err)
	}
	return s.sealer.Open(sealedValue, sealedDataKey)
}

// Delete removes a credential.
func (s *SecretStore) Delete(ctx context.Context, userID, name string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM secrets WHERE user_id = $1 AND name = $2`, userID, name)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
