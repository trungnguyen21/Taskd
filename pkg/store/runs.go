package store

import (
	"context"
	"errors"
	"time"

	"github.com/JyotinderSingh/task-queue/pkg/model"
	"github.com/jackc/pgconn"
	"github.com/jackc/pgx/v4"
	"github.com/jackc/pgx/v4/pgxpool"
)

// RunStore reads and writes runs.
type RunStore struct {
	pool *pgxpool.Pool
}

func NewRunStore(pool *pgxpool.Pool) *RunStore {
	return &RunStore{pool: pool}
}

const runColumns = `id, agent_id, schedule_id, trigger, status, scheduled_for,
	picked_at, started_at, finished_at, output, error, prompt_tokens,
	completion_tokens, created_at`

// Create records a run. Runs are created by the materializer when a schedule
// fires, and by the API when a user triggers one by hand.
func (s *RunStore) Create(ctx context.Context, userID, agentID string, scheduleID *string, trigger, status string, scheduledFor time.Time) (*model.Run, error) {
	row := s.pool.QueryRow(ctx, `INSERT INTO runs
		(user_id, agent_id, schedule_id, trigger, status, scheduled_for)
		VALUES ($1,$2,$3,$4,$5,$6)
		RETURNING `+runColumns,
		userID, agentID, scheduleID, trigger, status, scheduledFor)
	return scanRun(row)
}

// Get returns one run.
func (s *RunStore) Get(ctx context.Context, userID, id string) (*model.Run, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+runColumns+` FROM runs WHERE id = $1 AND user_id = $2`, id, userID)
	return scanRun(row)
}

// ListByAgent returns an agent's run history, newest first.
func (s *RunStore) ListByAgent(ctx context.Context, userID, agentID string, limit int) ([]*model.Run, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+runColumns+` FROM runs
		WHERE agent_id = $1 AND user_id = $2 ORDER BY scheduled_for DESC, created_at DESC LIMIT $3`,
		agentID, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	runs := []*model.Run{}
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func scanRun(row scanner) (*model.Run, error) {
	var run model.Run
	err := row.Scan(&run.ID, &run.AgentID, &run.ScheduleID, &run.Trigger, &run.Status,
		&run.ScheduledFor, &run.PickedAt, &run.StartedAt, &run.FinishedAt,
		&run.Output, &run.Error, &run.PromptTokens, &run.CompletionTokens, &run.CreatedAt)
	if err != nil {
		return nil, translateNoRows(err)
	}
	return &run, nil
}

// translateNoRows maps "there is no such row" onto ErrNotFound.
//
// An identifier that is not a valid UUID counts as not found rather than as a
// server error: from a caller's point of view asking for a nonsense id and
// asking for an id that was deleted are the same question.
func translateNoRows(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == invalidTextRepresentation {
		return ErrNotFound
	}
	return err
}

// invalidTextRepresentation is the Postgres error raised when a value cannot be
// cast to a column's type - here, a path parameter that is not a UUID.
const invalidTextRepresentation = "22P02"
