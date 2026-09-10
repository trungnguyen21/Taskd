package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v4/pgxpool"
)

// Step kinds.
const (
	StepModelCall = "model_call"
	StepToolCall  = "tool_call"
)

// Step is one entry in a run's trace: either what the model said, or a tool
// call with its arguments and what came back.
type Step struct {
	ID               string    `json:"id"`
	StepIndex        int       `json:"step_index"`
	Kind             string    `json:"kind"`
	Content          string    `json:"content"`
	ToolName         string    `json:"tool_name"`
	Arguments        string    `json:"arguments"`
	Result           string    `json:"result"`
	Error            string    `json:"error"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
	CreatedAt        time.Time `json:"created_at"`
}

// StepStore reads and writes run traces.
type StepStore struct {
	pool *pgxpool.Pool
}

func NewStepStore(pool *pgxpool.Pool) *StepStore {
	return &StepStore{pool: pool}
}

// Record appends one step. Steps are written as they happen rather than at the
// end, so a trace survives a crash mid-run.
func (s *StepStore) Record(ctx context.Context, runID string, index int, step Step) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO run_steps
		(run_id, step_index, kind, content, tool_name, arguments, result, error,
		 prompt_tokens, completion_tokens)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		runID, index, step.Kind, step.Content, step.ToolName, step.Arguments,
		step.Result, step.Error, step.PromptTokens, step.CompletionTokens)
	return err
}

// ListByRun returns a run's trace in order.
func (s *StepStore) ListByRun(ctx context.Context, userID, runID string) ([]*Step, error) {
	rows, err := s.pool.Query(ctx, `SELECT s.id, s.step_index, s.kind, s.content,
			s.tool_name, s.arguments, s.result, s.error, s.prompt_tokens,
			s.completion_tokens, s.created_at
		FROM run_steps s
		JOIN runs r ON r.id = s.run_id
		WHERE s.run_id = $1 AND r.user_id = $2
		ORDER BY s.step_index`, runID, userID)
	if err != nil {
		return nil, translateNoRows(err)
	}
	defer rows.Close()

	steps := []*Step{}
	for rows.Next() {
		var step Step
		if err := rows.Scan(&step.ID, &step.StepIndex, &step.Kind, &step.Content,
			&step.ToolName, &step.Arguments, &step.Result, &step.Error,
			&step.PromptTokens, &step.CompletionTokens, &step.CreatedAt); err != nil {
			return nil, err
		}
		steps = append(steps, &step)
	}
	return steps, rows.Err()
}
