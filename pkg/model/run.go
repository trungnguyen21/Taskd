package model

import "time"

// Run statuses. A run ends in exactly one of these, so that history can be
// scanned without reading every trace.
const (
	RunPending        = "pending"
	RunRunning        = "running"
	RunSucceeded      = "succeeded"
	RunFailed         = "failed"
	RunMissed         = "missed"
	RunCancelled      = "cancelled"
	RunBudgetExceeded = "budget_exceeded"
)

// What caused a run to exist.
const (
	TriggerSchedule = "schedule"
	TriggerManual   = "manual"
)

// Schedule is the recurrence attached to an agent.
type Schedule struct {
	ID             string     `json:"id"`
	AgentID        string     `json:"agent_id"`
	CronExpression string     `json:"cron_expression"`
	Timezone       string     `json:"timezone"`
	Enabled        bool       `json:"enabled"`
	NextFireAt     *time.Time `json:"next_fire_at"`
	LastFiredAt    *time.Time `json:"last_fired_at"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// Run is one execution of an agent.
type Run struct {
	ID               string     `json:"id"`
	AgentID          string     `json:"agent_id"`
	ScheduleID       *string    `json:"schedule_id"`
	Trigger          string     `json:"trigger"`
	Status           string     `json:"status"`
	ScheduledFor     time.Time  `json:"scheduled_for"`
	PickedAt         *time.Time `json:"picked_at"`
	StartedAt        *time.Time `json:"started_at"`
	FinishedAt       *time.Time `json:"finished_at"`
	Output           string     `json:"output"`
	Error            string     `json:"error"`
	PromptTokens     int        `json:"prompt_tokens"`
	CompletionTokens int        `json:"completion_tokens"`
	CreatedAt        time.Time  `json:"created_at"`
}
