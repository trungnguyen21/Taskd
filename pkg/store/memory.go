package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v4/pgxpool"
)

// Caps on what one namespace may hold. Unbounded memory silently inflates every
// run's token bill until an agent that cost pennies costs pounds, and it grows
// on the operator's disk forever.
const (
	MaxRecordsPerNamespace = 500
	MaxRecordBytes         = 16 * 1024
	// MaxReadLimit bounds a single read regardless of what the model asks for.
	MaxReadLimit = 50
)

// MemoryRecord is one thing an agent chose to remember.
type MemoryRecord struct {
	ID        string          `json:"id"`
	AgentID   string          `json:"agent_id"`
	Namespace string          `json:"namespace"`
	Key       string          `json:"key"`
	Content   json.RawMessage `json:"content"`
	CreatedAt time.Time       `json:"created_at"`
}

// MemoryStore reads and writes agent memory.
type MemoryStore struct {
	pool *pgxpool.Pool
}

func NewMemoryStore(pool *pgxpool.Pool) *MemoryStore {
	return &MemoryStore{pool: pool}
}

// Write appends a record and prunes the namespace back to its cap, oldest
// first. Memory is a log rather than a document store: writing does not fail
// once the cap is reached, it forgets the oldest thing.
func (s *MemoryStore) Write(ctx context.Context, userID, agentID, namespace, key string, content json.RawMessage) (*MemoryRecord, error) {
	if len(content) > MaxRecordBytes {
		return nil, fmt.Errorf("a memory record may be at most %d bytes", MaxRecordBytes)
	}

	var record MemoryRecord
	err := s.pool.QueryRow(ctx, `INSERT INTO memory_records
		(user_id, agent_id, namespace, key, content)
		VALUES ($1,$2,$3,$4,$5)
		RETURNING id, agent_id, namespace, key, content, created_at`,
		userID, agentID, namespace, key, content).
		Scan(&record.ID, &record.AgentID, &record.Namespace, &record.Key,
			&record.Content, &record.CreatedAt)
	if err != nil {
		return nil, translateNoRows(err)
	}

	if _, err := s.pool.Exec(ctx, `DELETE FROM memory_records
		WHERE user_id = $1 AND namespace = $2 AND id NOT IN (
			SELECT id FROM memory_records
			WHERE user_id = $1 AND namespace = $2
			ORDER BY created_at DESC, id DESC
			LIMIT $3
		)`, userID, namespace, MaxRecordsPerNamespace); err != nil {
		return nil, err
	}

	return &record, nil
}

// Read returns records from a namespace, newest first. The limit is required of
// the caller and capped here regardless of what was asked for.
func (s *MemoryStore) Read(ctx context.Context, userID, namespace, key string, limit int) ([]*MemoryRecord, error) {
	if limit < 1 {
		limit = 1
	}
	if limit > MaxReadLimit {
		limit = MaxReadLimit
	}

	query := `SELECT id, agent_id, namespace, key, content, created_at
		FROM memory_records WHERE user_id = $1 AND namespace = $2`
	arguments := []interface{}{userID, namespace}
	if key != "" {
		query += ` AND key = $4`
		arguments = append(arguments, limit, key)
	} else {
		arguments = append(arguments, limit)
	}
	query += ` ORDER BY created_at DESC, id DESC LIMIT $3`

	rows, err := s.pool.Query(ctx, query, arguments...)
	if err != nil {
		return nil, translateNoRows(err)
	}
	defer rows.Close()

	return scanMemoryRecords(rows)
}

// ListByAgent returns everything an agent has remembered, so a user can see and
// correct it.
func (s *MemoryStore) ListByAgent(ctx context.Context, userID, agentID string, limit int) ([]*MemoryRecord, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, agent_id, namespace, key, content, created_at
		FROM memory_records WHERE user_id = $1 AND agent_id = $2
		ORDER BY created_at DESC, id DESC LIMIT $3`, userID, agentID, limit)
	if err != nil {
		return nil, translateNoRows(err)
	}
	defer rows.Close()

	return scanMemoryRecords(rows)
}

// Delete removes one record, so a user can take out something wrong before it
// poisons every future run.
func (s *MemoryStore) Delete(ctx context.Context, userID, id string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM memory_records WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return translateNoRows(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// CountInNamespace is used to check the cap is holding.
func (s *MemoryStore) CountInNamespace(ctx context.Context, userID, namespace string) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM memory_records WHERE user_id = $1 AND namespace = $2`,
		userID, namespace).Scan(&count)
	return count, err
}

func scanMemoryRecords(rows interface {
	Next() bool
	Scan(...interface{}) error
	Err() error
}) ([]*MemoryRecord, error) {
	records := []*MemoryRecord{}
	for rows.Next() {
		var record MemoryRecord
		if err := rows.Scan(&record.ID, &record.AgentID, &record.Namespace,
			&record.Key, &record.Content, &record.CreatedAt); err != nil {
			return nil, err
		}
		records = append(records, &record)
	}
	return records, rows.Err()
}
