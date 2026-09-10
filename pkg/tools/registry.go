// Package tools is the first-party tool registry.
//
// The interface is deliberately MCP-shaped: a tool is a name, a description, a
// JSON Schema input and a handler returning content. Adding MCP servers later is
// then an adapter that registers into this same catalog, rather than a rewrite
// of the executor, the tool picker and the trace format.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/JyotinderSingh/task-queue/pkg/llm"
)

// Env is what a tool is allowed to know about the run invoking it.
type Env struct {
	RunID   string
	AgentID string
	UserID  string
}

// Tool is one capability an agent can be granted.
type Tool interface {
	Name() string
	Description() string
	// InputSchema is a JSON Schema object describing the tool's arguments.
	InputSchema() map[string]any
	// Execute returns the content handed back to the model. An error is
	// reported to the model as a tool result rather than ending the run, so a
	// model that can recover is given the chance to.
	Execute(ctx context.Context, arguments json.RawMessage, env Env) (string, error)
}

// Registry holds the tools an installation offers.
type Registry struct {
	tools map[string]Tool
}

func NewRegistry() *Registry {
	return &Registry{tools: map[string]Tool{}}
}

// Register adds a tool to the catalog.
func (r *Registry) Register(tool Tool) {
	r.tools[tool.Name()] = tool
}

// Get returns a tool by name.
func (r *Registry) Get(name string) (Tool, bool) {
	tool, ok := r.tools[name]
	return tool, ok
}

// Names lists the registered tools in a stable order.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Definitions renders the granted tools for the model. Tools an agent was not
// granted are not declared, so the model cannot ask for them.
func (r *Registry) Definitions(granted []string) []llm.ToolDefinition {
	definitions := []llm.ToolDefinition{}
	for _, name := range granted {
		tool, ok := r.tools[name]
		if !ok {
			continue
		}
		definitions = append(definitions, llm.ToolDefinition{
			Type: "function",
			Function: llm.FunctionSchema{
				Name:        tool.Name(),
				Description: tool.Description(),
				Parameters:  tool.InputSchema(),
			},
		})
	}
	return definitions
}

// ErrNotGranted is reported when a model asks for a tool the agent does not
// have. It is fed back to the model rather than ending the run.
func ErrNotGranted(name string) error {
	return fmt.Errorf("the tool %q is not available to this agent", name)
}
