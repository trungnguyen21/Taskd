// Package executor runs an agent: the tool-calling loop that is the actual work
// of the product.
package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/JyotinderSingh/task-queue/pkg/clock"
	"github.com/JyotinderSingh/task-queue/pkg/llm"
	"github.com/JyotinderSingh/task-queue/pkg/model"
	"github.com/JyotinderSingh/task-queue/pkg/store"
	"github.com/JyotinderSingh/task-queue/pkg/tools"
	"github.com/JyotinderSingh/task-queue/pkg/worker"
	"github.com/jackc/pgx/v4/pgxpool"
)

// Executor performs runs on behalf of a worker.
type Executor struct {
	pool     *pgxpool.Pool
	agents   *store.AgentStore
	runs     *store.RunStore
	steps    *store.StepStore
	registry *tools.Registry
	clock    clock.Clock
	// apiKey is the provider credential. It is supplied by the operator's
	// environment until stored credentials land.
	apiKey string
}

func New(pool *pgxpool.Pool, registry *tools.Registry, clk clock.Clock, apiKey string) *Executor {
	return &Executor{
		pool:     pool,
		agents:   store.NewAgentStore(pool),
		runs:     store.NewRunStore(pool),
		steps:    store.NewStepStore(pool),
		registry: registry,
		clock:    clk,
		apiKey:   apiKey,
	}
}

// Execute runs the agent's loop until it answers, or until a budget stops it.
//
// Every iteration is persisted before the next begins, so a trace survives a
// crash mid-run and a user can see where a run went wrong rather than only that
// it did.
func (e *Executor) Execute(ctx context.Context, runID, agentID string) (worker.Result, error) {
	agent, err := e.agents.Get(ctx, model.OwnerUserID, agentID)
	if err != nil {
		return worker.Result{}, fmt.Errorf("could not load the agent: %w", err)
	}

	messages, err := e.buildInitialMessages(ctx, agent)
	if err != nil {
		return worker.Result{}, err
	}

	// Storing the rendered prompt lets a user see what the model saw, rather
	// than what they think they wrote.
	if err := e.runs.SetRenderedPrompt(ctx, runID, renderPrompt(messages)); err != nil {
		return worker.Result{}, err
	}

	client := llm.New(agent.BaseURL, e.apiKey)
	definitions := e.registry.Definitions(agent.Tools)

	deadline := e.clock.Now().Add(time.Duration(agent.MaxDurationSeconds) * time.Second)
	result := worker.Result{}
	stepIndex := 0

	for step := 0; step < agent.MaxSteps; step++ {
		if e.clock.Now().After(deadline) {
			return e.stoppedByBudget(result, "the run exceeded its time limit"), nil
		}
		if result.PromptTokens+result.CompletionTokens > agent.MaxTokens {
			return e.stoppedByBudget(result, "the run exceeded its token limit"), nil
		}

		response, err := client.Complete(ctx, llm.Request{
			Model:    agent.Model,
			Messages: messages,
			Tools:    definitions,
		})
		if err != nil {
			return worker.Result{}, err
		}

		choice := response.Choices[0]
		result.PromptTokens += response.Usage.PromptTokens
		result.CompletionTokens += response.Usage.CompletionTokens

		if err := e.steps.Record(ctx, runID, stepIndex, store.Step{
			Kind:             store.StepModelCall,
			Content:          choice.Message.Content,
			PromptTokens:     response.Usage.PromptTokens,
			CompletionTokens: response.Usage.CompletionTokens,
		}); err != nil {
			return worker.Result{}, err
		}
		stepIndex++

		if len(choice.Message.ToolCalls) == 0 {
			result.Output = choice.Message.Content
			return result, nil
		}

		messages = append(messages, choice.Message)

		for _, call := range choice.Message.ToolCalls {
			content, toolErr := e.runTool(ctx, agent, call, tools.Env{
				RunID: runID, AgentID: agentID, UserID: model.OwnerUserID,
			})

			step := store.Step{
				Kind:      store.StepToolCall,
				ToolName:  call.Function.Name,
				Arguments: call.Function.Arguments,
			}
			if toolErr != nil {
				// A malformed or impossible tool call is recoverable. The error
				// goes back to the model as the tool's result so it can correct
				// itself, and the step still counts against the budget so a
				// model that cannot recover terminates instead of looping.
				step.Error = toolErr.Error()
				content = "Error: " + toolErr.Error()
			} else {
				step.Result = content
			}

			if err := e.steps.Record(ctx, runID, stepIndex, step); err != nil {
				return worker.Result{}, err
			}
			stepIndex++

			messages = append(messages, llm.Message{
				Role:       llm.RoleTool,
				ToolCallID: call.ID,
				Content:    content,
			})
		}
	}

	return e.stoppedByBudget(result, "the run exceeded its step limit"), nil
}

// runTool dispatches one tool call.
func (e *Executor) runTool(ctx context.Context, agent *model.Agent, call llm.ToolCall, env tools.Env) (string, error) {
	// A tool the agent was not granted is refused here as well as being absent
	// from the declarations, because a model is capable of inventing one.
	if !agent.HasTool(call.Function.Name) {
		return "", tools.ErrNotGranted(call.Function.Name)
	}

	tool, ok := e.registry.Get(call.Function.Name)
	if !ok {
		return "", tools.ErrNotGranted(call.Function.Name)
	}

	arguments := json.RawMessage(call.Function.Arguments)
	if len(strings.TrimSpace(call.Function.Arguments)) == 0 {
		arguments = json.RawMessage("{}")
	}
	if !json.Valid(arguments) {
		return "", fmt.Errorf("the arguments were not valid JSON")
	}

	return tool.Execute(ctx, arguments, env)
}

// buildInitialMessages assembles the conversation the model starts from.
func (e *Executor) buildInitialMessages(ctx context.Context, agent *model.Agent) ([]llm.Message, error) {
	messages := []llm.Message{}
	if agent.SystemPrompt != "" {
		messages = append(messages, llm.Message{Role: llm.RoleSystem, Content: agent.SystemPrompt})
	}

	if agent.ContextMode == model.ContextLastN && agent.ContextRuns > 0 {
		previous, err := e.runs.RecentOutputs(ctx, model.OwnerUserID, agent.ID, agent.ContextRuns)
		if err != nil {
			return nil, err
		}
		if len(previous) > 0 {
			messages = append(messages, llm.Message{
				Role:    llm.RoleUser,
				Content: "Output of your previous runs, most recent first:\n\n" + strings.Join(previous, "\n\n---\n\n"),
			})
		}
	}

	messages = append(messages, llm.Message{Role: llm.RoleUser, Content: agent.UserPrompt})
	return messages, nil
}

// stoppedByBudget marks a result as ended by its limits rather than by failure,
// keeping whatever the run produced before it stopped.
func (e *Executor) stoppedByBudget(result worker.Result, reason string) worker.Result {
	result.BudgetExceeded = true
	if result.Output == "" {
		result.Output = reason
	}
	return result
}

// renderPrompt flattens the starting conversation for the trace view.
func renderPrompt(messages []llm.Message) string {
	var builder strings.Builder
	for i, message := range messages {
		if i > 0 {
			builder.WriteString("\n\n")
		}
		builder.WriteString(strings.ToUpper(message.Role))
		builder.WriteString(":\n")
		builder.WriteString(message.Content)
	}
	return builder.String()
}
