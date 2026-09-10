// Package model holds the domain types shared by the API, the coordinator and
// the workers.
package model

import (
	"fmt"
	"time"
)

// Tool names in the first-party registry. The set is capability-shaped rather
// than service-shaped, so no entry is named after a vendor.
const (
	ToolWebSearch    = "web_search"
	ToolHTTPFetch    = "http_fetch"
	ToolMemoryRead   = "memory_read"
	ToolMemoryWrite  = "memory_write"
	ToolSendTelegram = "send_telegram"
	ToolSendWebhook  = "send_webhook"
)

// ToolCatalog is the set of tools an agent may be granted in v1.
var ToolCatalog = []string{
	ToolWebSearch,
	ToolHTTPFetch,
	ToolMemoryRead,
	ToolMemoryWrite,
	ToolSendTelegram,
	ToolSendWebhook,
}

// Context modes decide whether a run sees the output of previous runs.
const (
	ContextFresh = "fresh"
	ContextLastN = "last_n"
)

// Budget defaults applied to every new agent, so that a user is protected
// before they have thought about limits at all.
const (
	DefaultMaxSteps           = 20
	DefaultMaxTokens          = 100000
	DefaultMaxDurationSeconds = 600
)

// Budget ceilings. These are not overridable: an agent that could be configured
// without an upper bound is an unbounded bill.
const (
	CeilingMaxSteps           = 100
	CeilingMaxTokens          = 2000000
	CeilingMaxDurationSeconds = 3600
)

// OwnerUserID is the single user of a v1 install. Ownership is carried on every
// table from the start so that multi-user support is not a schema migration.
const OwnerUserID = "00000000-0000-0000-0000-000000000001"

// Agent is a scheduled unit of work: a model, a prompt, a set of granted tools
// and the limits it runs under.
type Agent struct {
	ID                 string    `json:"id"`
	Name               string    `json:"name"`
	Description        string    `json:"description"`
	Model              string    `json:"model"`
	BaseURL            string    `json:"base_url"`
	SystemPrompt       string    `json:"system_prompt"`
	UserPrompt         string    `json:"user_prompt"`
	Tools              []string  `json:"tools"`
	MaxSteps           int       `json:"max_steps"`
	MaxTokens          int       `json:"max_tokens"`
	MaxDurationSeconds int       `json:"max_duration_seconds"`
	ContextMode        string    `json:"context_mode"`
	ContextRuns        int       `json:"context_runs"`
	Enabled            bool      `json:"enabled"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// HasTool reports whether the agent was granted the named tool.
func (a *Agent) HasTool(name string) bool {
	for _, tool := range a.Tools {
		if tool == name {
			return true
		}
	}
	return false
}

// ApplyDefaults fills in the budget and context values a caller left unset.
func (a *Agent) ApplyDefaults() {
	if a.Tools == nil {
		a.Tools = []string{}
	}
	if a.MaxSteps == 0 {
		a.MaxSteps = DefaultMaxSteps
	}
	if a.MaxTokens == 0 {
		a.MaxTokens = DefaultMaxTokens
	}
	if a.MaxDurationSeconds == 0 {
		a.MaxDurationSeconds = DefaultMaxDurationSeconds
	}
	if a.ContextMode == "" {
		a.ContextMode = ContextFresh
	}
}

// Validate reports why an agent may not be stored, if it may not be.
func (a *Agent) Validate() error {
	if a.Name == "" {
		return fmt.Errorf("name is required")
	}
	if a.Model == "" {
		return fmt.Errorf("model is required")
	}
	for _, tool := range a.Tools {
		if !knownTool(tool) {
			return fmt.Errorf("unknown tool: %s", tool)
		}
	}
	switch a.ContextMode {
	case ContextFresh:
	case ContextLastN:
		if a.ContextRuns < 1 {
			return fmt.Errorf("context_runs must be at least 1 when context_mode is %s", ContextLastN)
		}
	default:
		return fmt.Errorf("context_mode must be %q or %q", ContextFresh, ContextLastN)
	}
	if a.MaxSteps < 1 || a.MaxSteps > CeilingMaxSteps {
		return fmt.Errorf("max_steps must be between 1 and %d", CeilingMaxSteps)
	}
	if a.MaxTokens < 1 || a.MaxTokens > CeilingMaxTokens {
		return fmt.Errorf("max_tokens must be between 1 and %d", CeilingMaxTokens)
	}
	if a.MaxDurationSeconds < 1 || a.MaxDurationSeconds > CeilingMaxDurationSeconds {
		return fmt.Errorf("max_duration_seconds must be between 1 and %d", CeilingMaxDurationSeconds)
	}
	return nil
}

func knownTool(name string) bool {
	for _, tool := range ToolCatalog {
		if tool == name {
			return true
		}
	}
	return false
}
