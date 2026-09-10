package store

import (
	"context"

	"github.com/JyotinderSingh/task-queue/pkg/model"
)

// AgentOverview is one row of the dashboard's home screen: the agent, the
// schedule attached to it, and how its most recent run ended.
//
// The agent is embedded rather than nested, so a list row is a superset of the
// agent representation the rest of the API returns and the dashboard has one
// shape to know rather than two.
type AgentOverview struct {
	model.Agent
	Schedule *model.Schedule `json:"schedule"`
	LastRun  *model.Run      `json:"last_run"`
}

// ListOverview returns every agent with its schedule and last run.
//
// The home screen asks "is anything broken", which is a question about all
// agents at once. Answering it per agent would be a request per row; this is
// three queries whatever the number of agents, stitched in memory because the
// result set is one person's worth of jobs.
func (s *AgentStore) ListOverview(ctx context.Context, userID string) ([]*AgentOverview, error) {
	agents, err := s.List(ctx, userID)
	if err != nil {
		return nil, err
	}

	schedules, err := s.schedulesByAgent(ctx, userID)
	if err != nil {
		return nil, err
	}
	lastRuns, err := s.lastRunByAgent(ctx, userID)
	if err != nil {
		return nil, err
	}

	overviews := []*AgentOverview{}
	for _, agent := range agents {
		overviews = append(overviews, &AgentOverview{
			Agent:    *agent,
			Schedule: schedules[agent.ID],
			LastRun:  lastRuns[agent.ID],
		})
	}
	return overviews, nil
}

func (s *AgentStore) schedulesByAgent(ctx context.Context, userID string) (map[string]*model.Schedule, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+scheduleColumns+` FROM schedules WHERE user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byAgent := map[string]*model.Schedule{}
	for rows.Next() {
		schedule, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		byAgent[schedule.AgentID] = schedule
	}
	return byAgent, rows.Err()
}

// lastRunByAgent returns each agent's most recent run.
//
// DISTINCT ON is what makes this one query rather than one per agent, and the
// ordering it takes is the same ordering the run history screen uses, so the
// row shown on the home screen is the row at the top of that list.
func (s *AgentStore) lastRunByAgent(ctx context.Context, userID string) (map[string]*model.Run, error) {
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT ON (agent_id) `+runColumns+`
		FROM runs WHERE user_id = $1
		ORDER BY agent_id, scheduled_for DESC, created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byAgent := map[string]*model.Run{}
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		byAgent[run.AgentID] = run
	}
	return byAgent, rows.Err()
}
