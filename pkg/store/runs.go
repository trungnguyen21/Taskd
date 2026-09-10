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
	completion_tokens, rendered_prompt, created_at`

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
		&run.Output, &run.Error, &run.PromptTokens, &run.CompletionTokens,
		&run.RenderedPrompt, &run.CreatedAt)
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

// SetRenderedPrompt stores the conversation the model was actually sent.
func (s *RunStore) SetRenderedPrompt(ctx context.Context, runID, prompt string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE runs SET rendered_prompt = $2 WHERE id = $1`, runID, prompt)
	return err
}

// RecentOutputs returns the output of an agent's last successful runs, newest
// first. It backs the last_n context mode.
func (s *RunStore) RecentOutputs(ctx context.Context, userID, agentID string, limit int) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT output FROM runs
		WHERE agent_id = $1 AND user_id = $2 AND status = $3 AND output <> ''
		ORDER BY finished_at DESC NULLS LAST LIMIT $4`,
		agentID, userID, model.RunSucceeded, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	outputs := []string{}
	for rows.Next() {
		var output string
		if err := rows.Scan(&output); err != nil {
			return nil, err
		}
		outputs = append(outputs, output)
	}
	return outputs, rows.Err()
}

// InboxItem is a run as the inbox shows it: what an agent produced, and whether
// it has been read.
type InboxItem struct {
	RunID     string     `json:"run_id"`
	AgentID   string     `json:"agent_id"`
	AgentName string     `json:"agent_name"`
	Status    string     `json:"status"`
	Output    string     `json:"output"`
	Error     string     `json:"error"`
	CreatedAt time.Time  `json:"created_at"`
	ReadAt    *time.Time `json:"read_at"`
}

// Inbox returns finished runs newest first, with the unread count.
//
// Every run's output is recorded whether or not the agent chose to notify, so
// this is useful before a delivery channel is configured and nothing is lost
// when a confused agent forgets to send.
func (s *RunStore) Inbox(ctx context.Context, userID string, limit int) ([]*InboxItem, int, error) {
	rows, err := s.pool.Query(ctx, `SELECT r.id, r.agent_id, a.name, r.status,
			r.output, r.error, r.created_at, r.read_at
		FROM runs r JOIN agents a ON a.id = r.agent_id
		WHERE r.user_id = $1 AND r.finished_at IS NOT NULL
		ORDER BY r.finished_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	items := []*InboxItem{}
	for rows.Next() {
		var item InboxItem
		if err := rows.Scan(&item.RunID, &item.AgentID, &item.AgentName, &item.Status,
			&item.Output, &item.Error, &item.CreatedAt, &item.ReadAt); err != nil {
			return nil, 0, err
		}
		items = append(items, &item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	var unread int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM runs
		WHERE user_id = $1 AND finished_at IS NOT NULL AND read_at IS NULL`,
		userID).Scan(&unread); err != nil {
		return nil, 0, err
	}

	return items, unread, nil
}

// MarkRead clears the unread flag on one run.
func (s *RunStore) MarkRead(ctx context.Context, userID, runID string, now time.Time) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE runs SET read_at = $3 WHERE id = $1 AND user_id = $2 AND read_at IS NULL`,
		runID, userID, now)
	if err != nil {
		return translateNoRows(err)
	}
	if tag.RowsAffected() == 0 {
		// Already read, or not there. Either way there is nothing to do, but a
		// missing run should still read as missing.
		var exists bool
		if err := s.pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM runs WHERE id = $1 AND user_id = $2)`,
			runID, userID).Scan(&exists); err != nil {
			return translateNoRows(err)
		}
		if !exists {
			return ErrNotFound
		}
	}
	return nil
}
