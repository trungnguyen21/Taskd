package store

import (
	"context"
	"time"

	"github.com/JyotinderSingh/task-queue/pkg/model"
	"github.com/jackc/pgx/v4/pgxpool"
)

// ScheduleStore reads and writes the recurrence attached to an agent.
type ScheduleStore struct {
	pool *pgxpool.Pool
}

func NewScheduleStore(pool *pgxpool.Pool) *ScheduleStore {
	return &ScheduleStore{pool: pool}
}

const scheduleColumns = `id, agent_id, cron_expression, timezone, enabled,
	next_fire_at, last_fired_at, created_at, updated_at`

// Upsert stores the schedule for an agent. An agent has at most one schedule in
// v1, so setting a new one replaces whatever was there.
func (s *ScheduleStore) Upsert(ctx context.Context, userID, agentID string, schedule *model.Schedule, nextFireAt time.Time, lastFiredWall string) (*model.Schedule, error) {
	row := s.pool.QueryRow(ctx, `INSERT INTO schedules
		(user_id, agent_id, cron_expression, timezone, enabled, next_fire_at, last_fired_wall)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (agent_id) DO UPDATE SET
			cron_expression = EXCLUDED.cron_expression,
			timezone = EXCLUDED.timezone,
			enabled = EXCLUDED.enabled,
			next_fire_at = EXCLUDED.next_fire_at,
			last_fired_wall = EXCLUDED.last_fired_wall,
			updated_at = NOW()
		RETURNING `+scheduleColumns,
		userID, agentID, schedule.CronExpression, schedule.Timezone, schedule.Enabled,
		nextFireAt, lastFiredWall)

	return scanSchedule(row)
}

// GetByAgent returns an agent's schedule.
func (s *ScheduleStore) GetByAgent(ctx context.Context, userID, agentID string) (*model.Schedule, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+scheduleColumns+` FROM schedules WHERE agent_id = $1 AND user_id = $2`,
		agentID, userID)
	return scanSchedule(row)
}

// DeleteByAgent removes an agent's schedule, leaving the agent itself in place.
func (s *ScheduleStore) DeleteByAgent(ctx context.Context, userID, agentID string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM schedules WHERE agent_id = $1 AND user_id = $2`, agentID, userID)
	if err != nil {
		return translateNoRows(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func scanSchedule(row scanner) (*model.Schedule, error) {
	var schedule model.Schedule
	err := row.Scan(&schedule.ID, &schedule.AgentID, &schedule.CronExpression,
		&schedule.Timezone, &schedule.Enabled, &schedule.NextFireAt,
		&schedule.LastFiredAt, &schedule.CreatedAt, &schedule.UpdatedAt)
	if err != nil {
		return nil, translateNoRows(err)
	}
	return &schedule, nil
}
