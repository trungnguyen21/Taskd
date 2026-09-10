package tests

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/JyotinderSingh/task-queue/pkg/store"
)

// agentUsing builds an agent pointed at the fake model endpoint.
func agentUsing(t *testing.T, model *fakeModel, name string, granted []string, overrides map[string]interface{}) string {
	t.Helper()

	payload := map[string]interface{}{
		"name":          name,
		"model":         "test-model",
		"base_url":      model.baseURL(),
		"system_prompt": "You are a scheduled agent.",
		"user_prompt":   "Do the thing.",
		"tools":         granted,
	}
	for key, value := range overrides {
		payload[key] = value
	}

	status, body := apiRequest(t, http.MethodPost, "/api/agents", payload)
	if status != http.StatusCreated {
		t.Fatalf("Expected 201 creating an agent, got %d: %s", status, body)
	}
	return decodeAgent(t, body)["id"].(string)
}

func listSteps(t *testing.T, runID string) []store.Step {
	t.Helper()
	status, body := apiRequest(t, http.MethodGet, "/api/runs/"+runID+"/steps", nil)
	if status != http.StatusOK {
		t.Fatalf("Expected 200 listing steps, got %d: %s", status, body)
	}
	var steps []store.Step
	if err := json.Unmarshal(body, &steps); err != nil {
		t.Fatalf("Failed to decode steps: %v (body: %s)", err, body)
	}
	return steps
}

func launchWithModel(t *testing.T) *fakeModel {
	t.Helper()
	model := newFakeModel()
	t.Cleanup(model.close)

	cluster = Cluster{}
	cluster.LaunchCluster(apiPort, coordinatorPort, 1)
	return model
}

// The simplest run: the model answers, and the answer plus the prompt it was
// actually sent are both readable afterwards.
func TestRunRecordsOutputPromptAndTokens(t *testing.T) {
	model := launchWithModel(t)
	defer teardown()

	model.queue(textResponse("Good morning. You have three meetings."))

	agentID := agentUsing(t, model, "Brief", nil, nil)
	runID := triggerRun(t, agentID)
	run := waitForRunStatus(t, runID, "succeeded")

	if run["output"] != "Good morning. You have three meetings." {
		t.Errorf("Expected the model's answer as the run output, got %v", run["output"])
	}
	if run["prompt_tokens"].(float64) != 11 || run["completion_tokens"].(float64) != 7 {
		t.Errorf("Expected the provider's token counts to be recorded, got %v and %v",
			run["prompt_tokens"], run["completion_tokens"])
	}

	prompt, _ := run["rendered_prompt"].(string)
	if !strings.Contains(prompt, "You are a scheduled agent.") || !strings.Contains(prompt, "Do the thing.") {
		t.Errorf("Expected the rendered prompt to show what the model saw, got %q", prompt)
	}

	steps := listSteps(t, runID)
	if len(steps) != 1 || steps[0].Kind != "model_call" {
		t.Fatalf("Expected a single model call in the trace, got %+v", steps)
	}
}

// The model calls a tool, sees the result, and answers with it.
func TestAgentCallsAToolAndSeesTheResult(t *testing.T) {
	model := launchWithModel(t)
	defer teardown()

	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Markets closed higher."))
	}))
	defer page.Close()

	model.queue(toolCallResponse("call-1", "http_fetch",
		`{"url":"`+page.URL+`"}`))
	model.queue(textResponse("Sentiment is positive."))

	agentID := agentUsing(t, model, "Sentiment", []string{"http_fetch"}, nil)
	runID := triggerRun(t, agentID)
	run := waitForRunStatus(t, runID, "succeeded")

	if run["output"] != "Sentiment is positive." {
		t.Errorf("Expected the final answer as output, got %v", run["output"])
	}

	steps := listSteps(t, runID)
	if len(steps) != 3 {
		t.Fatalf("Expected a model call, a tool call and a model call, got %d steps", len(steps))
	}
	if steps[1].Kind != "tool_call" || steps[1].ToolName != "http_fetch" {
		t.Fatalf("Expected the second step to be the tool call, got %+v", steps[1])
	}
	if !strings.Contains(steps[1].Arguments, page.URL) {
		t.Errorf("Expected the tool call to record its arguments, got %q", steps[1].Arguments)
	}
	if !strings.Contains(steps[1].Result, "Markets closed higher.") {
		t.Errorf("Expected the tool call to record its result, got %q", steps[1].Result)
	}
	if steps[1].Error != "" {
		t.Errorf("Expected the tool call to succeed, got error %q", steps[1].Error)
	}

	// The tool result was fed back, which is what lets the model use it.
	requests := model.requestsReceived()
	if len(requests) != 2 {
		t.Fatalf("Expected two model calls, got %d", len(requests))
	}
	var sawResult bool
	for _, message := range requests[1].Messages {
		if strings.Contains(message.Content, "Markets closed higher.") {
			sawResult = true
		}
	}
	if !sawResult {
		t.Error("Expected the tool result to be fed back to the model")
	}
}

// Local models routinely emit tool calls that are not valid JSON. The error goes
// back to the model so it can correct itself, and the run continues.
func TestMalformedToolArgumentsAreRecoverable(t *testing.T) {
	model := launchWithModel(t)
	defer teardown()

	model.queue(toolCallResponse("call-1", "http_fetch", `{"url": htt`))
	model.queue(textResponse("Recovered without the page."))

	agentID := agentUsing(t, model, "Clumsy", []string{"http_fetch"}, nil)
	runID := triggerRun(t, agentID)
	run := waitForRunStatus(t, runID, "succeeded")

	if run["output"] != "Recovered without the page." {
		t.Errorf("Expected the run to continue after a malformed call, got %v", run["output"])
	}

	steps := listSteps(t, runID)
	if len(steps) != 3 {
		t.Fatalf("Expected the failed tool call to be recorded as a step, got %d steps", len(steps))
	}
	if steps[1].Error == "" {
		t.Error("Expected the malformed call to record why it failed")
	}

	var sawError bool
	for _, message := range model.requestsReceived()[1].Messages {
		if strings.Contains(strings.ToLower(message.Content), "error") {
			sawError = true
		}
	}
	if !sawError {
		t.Error("Expected the error to be fed back so the model could correct itself")
	}
}

// A model can invent a tool. An agent may only use what it was granted.
func TestUngrantedToolIsRefused(t *testing.T) {
	model := launchWithModel(t)
	defer teardown()

	model.queue(toolCallResponse("call-1", "http_fetch", `{"url":"https://example.com"}`))
	model.queue(textResponse("Answered without it."))

	// No tools granted at all.
	agentID := agentUsing(t, model, "Ungranted", nil, nil)
	runID := triggerRun(t, agentID)
	waitForRunStatus(t, runID, "succeeded")

	steps := listSteps(t, runID)
	if len(steps) != 3 {
		t.Fatalf("Expected the refusal to be recorded, got %d steps", len(steps))
	}
	if !strings.Contains(steps[1].Error, "not available") {
		t.Errorf("Expected the tool to be refused, got %q", steps[1].Error)
	}
	if steps[1].Result != "" {
		t.Error("Expected no result from a tool that was never run")
	}

	// The declarations offered to the model contained nothing.
	if declared := model.requestsReceived()[0].Tools; len(declared) != 0 {
		t.Errorf("Expected no tools to be declared to the model, got %d", len(declared))
	}
}

// A model that never stops calling tools is stopped by the step budget rather
// than running until someone notices the bill.
func TestStepBudgetStopsARunawayRun(t *testing.T) {
	model := launchWithModel(t)
	defer teardown()

	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("more"))
	}))
	defer page.Close()

	model.always(toolCallResponse("call-loop", "http_fetch", `{"url":"`+page.URL+`"}`))

	agentID := agentUsing(t, model, "Runaway", []string{"http_fetch"},
		map[string]interface{}{"max_steps": 3})
	runID := triggerRun(t, agentID)
	run := waitForRunStatus(t, runID, "budget_exceeded")

	if calls := model.calls(); calls != 3 {
		t.Errorf("Expected the loop to stop after 3 model calls, got %d", calls)
	}
	if run["finished_at"] == nil {
		t.Error("Expected a budgeted run to be finished, not left running")
	}
}

// The limit is enforced by the platform, not requested of the model: an agent
// asking for more than the ceiling is refused when it is created.
func TestBudgetCeilingIsNotOverridable(t *testing.T) {
	model := launchWithModel(t)
	defer teardown()

	status, body := apiRequest(t, http.MethodPost, "/api/agents", map[string]interface{}{
		"name": "Greedy", "model": "test-model", "base_url": model.baseURL(),
		"max_steps": 100000,
	})
	if status != http.StatusBadRequest {
		t.Fatalf("Expected the ceiling to be enforced, got %d: %s", status, body)
	}
}

// A provider that rejects the call fails the run and records why, so a bad key
// is distinguishable from a tool timeout.
func TestModelFailureFailsTheRunWithAReason(t *testing.T) {
	model := launchWithModel(t)
	defer teardown()

	model.failWithStatus(http.StatusUnauthorized)

	agentID := agentUsing(t, model, "Misconfigured", nil, nil)
	runID := triggerRun(t, agentID)
	run := waitForRunStatus(t, runID, "failed")

	errorText, _ := run["error"].(string)
	if !strings.Contains(errorText, "401") {
		t.Errorf("Expected the provider's rejection to be recorded, got %q", errorText)
	}
}

// An agent set to last_n sees what it produced before, so a scheduled job can
// report on what changed rather than describing the world afresh.
func TestPreviousOutputIsIncludedWhenContextIsLastN(t *testing.T) {
	model := launchWithModel(t)
	defer teardown()

	model.queue(textResponse("Yesterday: risk-on."))
	model.queue(textResponse("Today: cooler than yesterday."))

	agentID := agentUsing(t, model, "Contextual", nil, map[string]interface{}{
		"context_mode": "last_n", "context_runs": 3,
	})

	waitForRunStatus(t, triggerRun(t, agentID), "succeeded")
	waitForRunStatus(t, triggerRun(t, agentID), "succeeded")

	requests := model.requestsReceived()
	if len(requests) != 2 {
		t.Fatalf("Expected two runs to make two model calls, got %d", len(requests))
	}

	var sawPrevious bool
	for _, message := range requests[1].Messages {
		if strings.Contains(message.Content, "Yesterday: risk-on.") {
			sawPrevious = true
		}
	}
	if !sawPrevious {
		t.Error("Expected the second run to see the first run's output")
	}

	// A fresh agent must not: the default is deliberately fresh, so one bad run
	// cannot contaminate every run after it.
	freshModel := newFakeModel()
	defer freshModel.close()
	freshModel.queue(textResponse("first"))
	freshModel.queue(textResponse("second"))

	freshID := agentUsing(t, freshModel, "Fresh", nil, nil)
	waitForRunStatus(t, triggerRun(t, freshID), "succeeded")
	waitForRunStatus(t, triggerRun(t, freshID), "succeeded")

	for _, message := range freshModel.requestsReceived()[1].Messages {
		if strings.Contains(message.Content, "first") {
			t.Error("Expected a fresh agent not to see its previous output")
		}
	}
}

// The fetch tool refuses to be pointed at the deployment's own network.
func TestHTTPFetchRefusesMetadataAddresses(t *testing.T) {
	model := launchWithModel(t)
	defer teardown()

	model.queue(toolCallResponse("call-1", "http_fetch",
		`{"url":"http://169.254.169.254/latest/meta-data/"}`))
	model.queue(textResponse("Could not read that."))

	agentID := agentUsing(t, model, "Curious", []string{"http_fetch"}, nil)
	runID := triggerRun(t, agentID)
	waitForRunStatus(t, runID, "succeeded")

	steps := listSteps(t, runID)
	if steps[1].Error == "" {
		t.Fatal("Expected the metadata address to be refused")
	}
	if strings.Contains(steps[1].Result, "meta-data") {
		t.Error("Expected nothing to be returned from the metadata endpoint")
	}
}
