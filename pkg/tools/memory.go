package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/JyotinderSingh/task-queue/pkg/store"
)

// MemoryWrite lets an agent record something for its later runs.
//
// There is no "has memory" setting on an agent: an agent has memory exactly
// when these tools are among its grants. Capabilities are grants, not settings.
type MemoryWrite struct {
	store *store.MemoryStore
}

func NewMemoryWrite(memory *store.MemoryStore) *MemoryWrite {
	return &MemoryWrite{store: memory}
}

func (MemoryWrite) Name() string { return "memory_write" }

func (MemoryWrite) Description() string {
	return "Remember something for your future runs. Records are kept newest-first under a key, " +
		"so later runs can compare what changed rather than describing the world afresh."
}

func (MemoryWrite) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"key": map[string]any{
				"type":        "string",
				"description": "A short label for this record, for example 'sentiment' or 'workout'.",
			},
			"content": map[string]any{
				"description": "The value to remember. May be any JSON value.",
			},
			"namespace": map[string]any{
				"type":        "string",
				"description": "Optional. Defaults to this agent's own private memory.",
			},
		},
		"required": []string{"key", "content"},
	}
}

func (m *MemoryWrite) Execute(ctx context.Context, arguments json.RawMessage, env Env) (string, error) {
	var input struct {
		Key       string          `json:"key"`
		Content   json.RawMessage `json:"content"`
		Namespace string          `json:"namespace"`
	}
	if err := json.Unmarshal(arguments, &input); err != nil {
		return "", fmt.Errorf("arguments were not valid JSON: %w", err)
	}
	if strings.TrimSpace(input.Key) == "" {
		return "", fmt.Errorf("a key is required")
	}
	if len(input.Content) == 0 {
		return "", fmt.Errorf("content is required")
	}

	record, err := m.store.Write(ctx, env.UserID, env.AgentID,
		namespaceOrDefault(input.Namespace, env), input.Key, input.Content)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("Remembered %q in namespace %q.", record.Key, record.Namespace), nil
}

// MemoryRead lets an agent read back what it remembered.
type MemoryRead struct {
	store *store.MemoryStore
}

func NewMemoryRead(memory *store.MemoryStore) *MemoryRead {
	return &MemoryRead{store: memory}
}

func (MemoryRead) Name() string { return "memory_read" }

func (MemoryRead) Description() string {
	return "Read back what you remembered on earlier runs, newest first."
}

func (MemoryRead) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"limit": map[string]any{
				"type":        "integer",
				"description": fmt.Sprintf("How many records to read. Required, and capped at %d.", store.MaxReadLimit),
			},
			"key": map[string]any{
				"type":        "string",
				"description": "Optional. Only return records stored under this key.",
			},
			"namespace": map[string]any{
				"type":        "string",
				"description": "Optional. Defaults to this agent's own private memory.",
			},
		},
		"required": []string{"limit"},
	}
}

func (m *MemoryRead) Execute(ctx context.Context, arguments json.RawMessage, env Env) (string, error) {
	var input struct {
		Limit     int    `json:"limit"`
		Key       string `json:"key"`
		Namespace string `json:"namespace"`
	}
	if err := json.Unmarshal(arguments, &input); err != nil {
		return "", fmt.Errorf("arguments were not valid JSON: %w", err)
	}
	// The limit is required of the model, but a model that omits it should not
	// end the run over it.
	if input.Limit < 1 {
		input.Limit = 10
	}

	records, err := m.store.Read(ctx, env.UserID,
		namespaceOrDefault(input.Namespace, env), input.Key, input.Limit)
	if err != nil {
		return "", err
	}
	if len(records) == 0 {
		return "No records found.", nil
	}

	var builder strings.Builder
	for _, record := range records {
		fmt.Fprintf(&builder, "%s  %s: %s\n",
			record.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
			record.Key, string(record.Content))
	}
	return strings.TrimRight(builder.String(), "\n"), nil
}

// namespaceOrDefault falls back to the agent's own private namespace.
func namespaceOrDefault(namespace string, env Env) string {
	if strings.TrimSpace(namespace) == "" {
		return env.AgentID
	}
	return namespace
}
