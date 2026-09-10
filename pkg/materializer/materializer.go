// Package materializer turns schedules into concrete runs. It is the piece the
// inherited scheduler never had: every task there was a single instant, retired
// once dispatched.
package materializer

import (
	"context"
	"log"
	"time"

	"github.com/JyotinderSingh/task-queue/pkg/clock"
	"github.com/JyotinderSingh/task-queue/pkg/model"
	"github.com/JyotinderSingh/task-queue/pkg/schedule"
	"github.com/jackc/pgx/v4/pgxpool"
)

// GraceWindow is how late a fire time may be and still be executed. Past it the
// run is recorded as missed instead: a 7am briefing delivered at 9am is worse
// than none.
const GraceWindow = 5 * time.Minute

// maxCatchUpIterations bounds the walk forward through fire times that were
// missed while the system was down.
const maxCatchUpIterations = 100000

// Materializer expands due schedules into runs.
type Materializer struct {
	pool  *pgxpool.Pool
	clock clock.Clock
}

func New(pool *pgxpool.Pool, clk clock.Clock) *Materializer {
	return &Materializer{pool: pool, clock: clk}
}

type dueSchedule struct {
	id            string
	userID        string
	agentID       string
	expression    string
	timezone      string
	nextFireAt    time.Time
	lastFiredWall string
}

// RunOnce materializes every schedule that is due, and returns how many runs it
// created.
func (m *Materializer) RunOnce(ctx context.Context) (int, error) {
	now := m.clock.Now()

	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	// SKIP LOCKED so that more than one coordinator can scan concurrently
	// without both materializing the same schedule.
	rows, err := tx.Query(ctx, `SELECT s.id, s.user_id, s.agent_id, s.cron_expression,
			s.timezone, s.next_fire_at, s.last_fired_wall
		FROM schedules s
		JOIN agents a ON a.id = s.agent_id
		WHERE s.enabled AND a.enabled AND s.next_fire_at IS NOT NULL AND s.next_fire_at <= $1
		ORDER BY s.next_fire_at
		FOR UPDATE OF s SKIP LOCKED`, now)
	if err != nil {
		return 0, err
	}

	due := []dueSchedule{}
	for rows.Next() {
		var item dueSchedule
		if err := rows.Scan(&item.id, &item.userID, &item.agentID, &item.expression,
			&item.timezone, &item.nextFireAt, &item.lastFiredWall); err != nil {
			rows.Close()
			return 0, err
		}
		due = append(due, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	created := 0
	for _, item := range due {
		spec, err := schedule.Parse(item.expression, item.timezone)
		if err != nil {
			// A schedule that no longer parses cannot be advanced; leaving it
			// alone keeps it visible rather than silently dropping it.
			log.Printf("Schedule %s is not parseable, skipping: %v", item.id, err)
			continue
		}

		fireAt, nextFireAt, missed := resolveFireTimes(spec, item, now)

		status := model.RunPending
		if missed {
			status = model.RunMissed
		}

		if _, err := tx.Exec(ctx, `INSERT INTO runs
			(user_id, agent_id, schedule_id, trigger, status, scheduled_for)
			VALUES ($1,$2,$3,$4,$5,$6)`,
			item.userID, item.agentID, item.id, model.TriggerSchedule, status, fireAt); err != nil {
			return 0, err
		}
		created++

		if _, err := tx.Exec(ctx, `UPDATE schedules
			SET next_fire_at = $2, last_fired_at = $3, last_fired_wall = $4, updated_at = NOW()
			WHERE id = $1`,
			item.id, nullableTime(nextFireAt), fireAt, spec.WallClock(fireAt)); err != nil {
			return 0, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return created, nil
}

// resolveFireTimes decides which fire time this schedule is being materialized
// for, and when it should fire next.
//
// Fire times that passed while nothing was running collapse into a single
// missed run rather than a row per missed occurrence: an outage over a weekend
// should leave one record saying the schedule was missed, not a thousand.
func resolveFireTimes(spec *schedule.Spec, item dueSchedule, now time.Time) (fireAt time.Time, nextFireAt time.Time, missed bool) {
	fireAt = item.nextFireAt

	for i := 0; i < maxCatchUpIterations; i++ {
		next := spec.Next(fireAt, spec.WallClock(fireAt))
		if next.IsZero() {
			nextFireAt = time.Time{}
			break
		}
		if next.After(now) {
			nextFireAt = next
			break
		}
		// Another fire time is already in the past. Only the most recent one is
		// worth recording, so the earlier occurrences are dropped rather than
		// producing a row each.
		fireAt = next
	}

	// The run executes only if the fire time it is being materialized for is
	// still fresh. Past the grace window it is recorded and not run.
	missed = now.Sub(fireAt) > GraceWindow
	return fireAt, nextFireAt, missed
}

func nullableTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
