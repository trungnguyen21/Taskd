// Package store holds the database access for the domain types. Postgres is the
// system of record, and every service reads it directly.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/JyotinderSingh/task-queue/pkg/model"
	"github.com/jackc/pgx/v4/pgxpool"
)

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("not found")

// AgentStore reads and writes agents.
type AgentStore struct {
	pool *pgxpool.Pool
}

func NewAgentStore(pool *pgxpool.Pool) *AgentStore {
	return &AgentStore{pool: pool}
}

const agentColumns = `id, name, description, model, base_url, system_prompt, user_prompt,
	tools, max_steps, max_tokens, max_duration_seconds, context_mode, context_runs,
	secret_name, enabled, created_at, updated_at`

// Create stores a new agent and returns it as persisted.
func (s *AgentStore) Create(ctx context.Context, userID string, agent *model.Agent) (*model.Agent, error) {
	tools, err := json.Marshal(agent.Tools)
	if err != nil {
		return nil, err
	}

	row := s.pool.QueryRow(ctx, `INSERT INTO agents
		(user_id, name, description, model, base_url, system_prompt, user_prompt, tools,
		 max_steps, max_tokens, max_duration_seconds, context_mode, context_runs,
		 secret_name, enabled)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		RETURNING `+agentColumns,
		userID, agent.Name, agent.Description, agent.Model, agent.BaseURL,
		agent.SystemPrompt, agent.UserPrompt, tools, agent.MaxSteps, agent.MaxTokens,
		agent.MaxDurationSeconds, agent.ContextMode, agent.ContextRuns,
		agent.SecretName, agent.Enabled)

	return scanAgent(row)
}

// Get returns one agent owned by the user.
func (s *AgentStore) Get(ctx context.Context, userID, id string) (*model.Agent, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+agentColumns+` FROM agents WHERE id = $1 AND user_id = $2`, id, userID)
	return scanAgent(row)
}

// List returns the user's agents, newest first.
func (s *AgentStore) List(ctx context.Context, userID string) ([]*model.Agent, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+agentColumns+` FROM agents WHERE user_id = $1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	agents := []*model.Agent{}
	for rows.Next() {
		agent, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		agents = append(agents, agent)
	}
	return agents, rows.Err()
}

// Update replaces the mutable fields of an existing agent. The change applies to
// the agent's next run; runs already in flight are unaffected.
func (s *AgentStore) Update(ctx context.Context, userID, id string, agent *model.Agent) (*model.Agent, error) {
	tools, err := json.Marshal(agent.Tools)
	if err != nil {
		return nil, err
	}

	row := s.pool.QueryRow(ctx, `UPDATE agents SET
		name = $3, description = $4, model = $5, base_url = $6, system_prompt = $7,
		user_prompt = $8, tools = $9, max_steps = $10, max_tokens = $11,
		max_duration_seconds = $12, context_mode = $13, context_runs = $14,
		secret_name = $15, enabled = $16, updated_at = NOW()
		WHERE id = $1 AND user_id = $2
		RETURNING `+agentColumns,
		id, userID, agent.Name, agent.Description, agent.Model, agent.BaseURL,
		agent.SystemPrompt, agent.UserPrompt, tools, agent.MaxSteps, agent.MaxTokens,
		agent.MaxDurationSeconds, agent.ContextMode, agent.ContextRuns,
		agent.SecretName, agent.Enabled)

	return scanAgent(row)
}

// Delete removes an agent. Reports ErrNotFound if it was not there to remove.
func (s *AgentStore) Delete(ctx context.Context, userID, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM agents WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return translateNoRows(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// scanner is satisfied by both pgx.Row and pgx.Rows.
type scanner interface {
	Scan(dest ...interface{}) error
}

func scanAgent(row scanner) (*model.Agent, error) {
	var agent model.Agent
	var tools []byte

	err := row.Scan(&agent.ID, &agent.Name, &agent.Description, &agent.Model,
		&agent.BaseURL, &agent.SystemPrompt, &agent.UserPrompt, &tools,
		&agent.MaxSteps, &agent.MaxTokens, &agent.MaxDurationSeconds,
		&agent.ContextMode, &agent.ContextRuns, &agent.SecretName, &agent.Enabled,
		&agent.CreatedAt, &agent.UpdatedAt)
	if err != nil {
		return nil, translateNoRows(err)
	}

	if err := json.Unmarshal(tools, &agent.Tools); err != nil {
		return nil, fmt.Errorf("decoding tool grants: %w", err)
	}
	if agent.Tools == nil {
		agent.Tools = []string{}
	}
	return &agent, nil
}
