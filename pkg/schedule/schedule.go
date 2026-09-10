// Package schedule turns a cron expression and an IANA timezone into concrete
// fire times.
package schedule

import (
	"fmt"
	"time"

	// Embeds the timezone database in the binary. Minimal container images do
	// not carry one, and without this every schedule fails at runtime while
	// working correctly on a developer machine.
	_ "time/tzdata"

	"github.com/robfig/cron/v3"
)

// MinimumInterval is the finest granularity a schedule may ask for. Agent runs
// take tens of seconds and cost money; per-minute scheduling would imply a
// real-time system nobody asked for.
const MinimumInterval = 5 * time.Minute

// wallClockLayout is the form used to compare two fire times as the user would
// read them off a clock, ignoring which UTC offset was in force.
const wallClockLayout = "2006-01-02T15:04:05"

// Spec is a parsed schedule.
type Spec struct {
	Expression string
	Location   *time.Location
	schedule   cron.Schedule
}

// Parse validates a cron expression and timezone together.
func Parse(expression, timezone string) (*Spec, error) {
	if timezone == "" {
		return nil, fmt.Errorf("timezone is required")
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, fmt.Errorf("unknown timezone %q", timezone)
	}

	parsed, err := cron.ParseStandard(expression)
	if err != nil {
		return nil, fmt.Errorf("invalid cron expression: %w", err)
	}

	spec := &Spec{Expression: expression, Location: location, schedule: parsed}
	if err := spec.checkGranularity(); err != nil {
		return nil, err
	}
	return spec, nil
}

// checkGranularity rejects schedules that fire more often than MinimumInterval.
func (s *Spec) checkGranularity() error {
	// A reference instant well away from any DST transition, so that the gap
	// being measured is the schedule's own and not a clock change.
	reference := time.Date(2025, time.June, 1, 0, 0, 0, 0, s.Location)

	first := s.schedule.Next(reference)
	if first.IsZero() {
		return fmt.Errorf("cron expression never fires")
	}
	second := s.schedule.Next(first)
	if second.IsZero() {
		return nil
	}

	if second.Sub(first) < MinimumInterval {
		return fmt.Errorf("schedule fires more often than every %s", MinimumInterval)
	}
	return nil
}

// WallClock renders an instant the way the user reads it in their own zone.
func (s *Spec) WallClock(t time.Time) string {
	return t.In(s.Location).Format(wallClockLayout)
}

// Next returns the first fire time strictly after `after`, expressed in the
// schedule's zone.
//
// Two daylight-saving hazards are handled here. In spring an hour does not
// exist, so a schedule pointing into it finds no match that day and is not
// fired. In autumn an hour occurs twice, and the naive answer fires the
// schedule twice that day: both instants are real, and the second is genuinely
// later than the first, so ordering alone does not separate them. They are
// separated by wall-clock reading instead - a fire time the user would read
// identically to the last one is skipped.
func (s *Spec) Next(after time.Time, lastFiredWall string) time.Time {
	candidate := s.schedule.Next(after.In(s.Location))
	if candidate.IsZero() {
		return time.Time{}
	}

	if lastFiredWall != "" && s.WallClock(candidate) == lastFiredWall {
		candidate = s.schedule.Next(candidate)
		if candidate.IsZero() {
			return time.Time{}
		}
	}
	return candidate.In(s.Location)
}
