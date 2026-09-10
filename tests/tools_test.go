package tests

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/JyotinderSingh/task-queue/pkg/store"
	"github.com/JyotinderSingh/task-queue/pkg/tools"
)

// recorder captures what a tool sent to an external service.
type recorder struct {
	mu       sync.Mutex
	bodies   []string
	paths    []string
	response string
	status   int
}

func newRecorder(response string) (*recorder, *httptest.Server) {
	rec := &recorder{response: response, status: http.StatusOK}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, r.ContentLength)
		if r.ContentLength > 0 {
			r.Body.Read(body)
		}

		rec.mu.Lock()
		rec.bodies = append(rec.bodies, string(body))
		rec.paths = append(rec.paths, r.URL.Path+"?"+r.URL.RawQuery)
		status, response := rec.status, rec.response
		rec.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write([]byte(response))
	}))
	return rec, server
}

func (r *recorder) received() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.bodies...)
}

func (r *recorder) requestedPaths() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.paths...)
}

func listMemory(t *testing.T, agentID string) []store.MemoryRecord {
	t.Helper()
	status, body := apiRequest(t, http.MethodGet, "/api/agents/"+agentID+"/memory", nil)
	if status != http.StatusOK {
		t.Fatalf("Expected 200 listing memory, got %d: %s", status, body)
	}
	var records []store.MemoryRecord
	if err := json.Unmarshal(body, &records); err != nil {
		t.Fatalf("Failed to decode memory records: %v (body: %s)", err, body)
	}
	return records
}

// An agent writes something on one run and reads it back on the next, which is
// what lets a scheduled job report on change rather than re-observing the world.
func TestAgentRemembersAcrossRuns(t *testing.T) {
	model := launchWithModel(t)
	defer teardown()

	model.queue(toolCallResponse("call-1", "memory_write",
		`{"key":"sentiment","content":{"mood":"risk-on"}}`))
	model.queue(textResponse("Recorded today's sentiment."))
	model.queue(toolCallResponse("call-2", "memory_read", `{"limit":5}`))
	model.queue(textResponse("Cooler than yesterday."))

	agentID := agentUsing(t, model, "Sentiment", []string{"memory_read", "memory_write"}, nil)

	waitForRunStatus(t, triggerRun(t, agentID), "succeeded")
	secondRun := triggerRun(t, agentID)
	waitForRunStatus(t, secondRun, "succeeded")

	steps := listSteps(t, secondRun)
	if len(steps) < 2 || steps[1].ToolName != "memory_read" {
		t.Fatalf("Expected the second run to read memory, got %+v", steps)
	}
	if !strings.Contains(steps[1].Result, "risk-on") {
		t.Errorf("Expected the earlier record to be read back, got %q", steps[1].Result)
	}
	if steps[1].Error != "" {
		t.Errorf("Expected the read to succeed, got %q", steps[1].Error)
	}

	// The user can see and correct what the agent remembered.
	records := listMemory(t, agentID)
	if len(records) != 1 {
		t.Fatalf("Expected one memory record, got %d", len(records))
	}
	if records[0].Key != "sentiment" || !strings.Contains(string(records[0].Content), "risk-on") {
		t.Errorf("Expected the stored record to round-trip, got %+v", records[0])
	}

	status, _ := apiRequest(t, http.MethodDelete, "/api/memory/"+records[0].ID, nil)
	if status != http.StatusNoContent {
		t.Fatalf("Expected 204 deleting a memory record, got %d", status)
	}
	if remaining := listMemory(t, agentID); len(remaining) != 0 {
		t.Fatalf("Expected the record to be deleted, got %d", len(remaining))
	}
}

// Memory is a grant, not a setting: an agent without the tools cannot reach it.
func TestAgentWithoutMemoryToolsCannotReachMemory(t *testing.T) {
	model := launchWithModel(t)
	defer teardown()

	model.queue(toolCallResponse("call-1", "memory_write", `{"key":"x","content":1}`))
	model.queue(textResponse("Could not remember that."))

	agentID := agentUsing(t, model, "Stateless", []string{"http_fetch"}, nil)
	runID := triggerRun(t, agentID)
	waitForRunStatus(t, runID, "succeeded")

	steps := listSteps(t, runID)
	if !strings.Contains(steps[1].Error, "not available") {
		t.Errorf("Expected memory to be refused, got %q", steps[1].Error)
	}
	if records := listMemory(t, agentID); len(records) != 0 {
		t.Fatalf("Expected nothing to have been written, got %d records", len(records))
	}
}

// Memory does not grow without bound: past the cap the oldest record is
// forgotten rather than the write failing.
func TestMemoryNamespaceIsCapped(t *testing.T) {
	cluster = Cluster{}
	cluster.LaunchAPI(apiPort)
	defer teardown()

	agentID := createAgent(t, "Verbose")
	memory := store.NewMemoryStore(cluster.DB)

	total := store.MaxRecordsPerNamespace + 10
	for i := 0; i < total; i++ {
		content := json.RawMessage(`{"n":` + string(rune('0'+i%10)) + `}`)
		if _, err := memory.Write(t.Context(), ownerUserID, agentID, agentID, "tick", content); err != nil {
			t.Fatalf("Failed to write memory: %v", err)
		}
	}

	count, err := memory.CountInNamespace(t.Context(), ownerUserID, agentID)
	if err != nil {
		t.Fatalf("Failed to count memory: %v", err)
	}
	if count != store.MaxRecordsPerNamespace {
		t.Fatalf("Expected the namespace to be capped at %d, got %d",
			store.MaxRecordsPerNamespace, count)
	}

	// A read is bounded regardless of what is asked for.
	records, err := memory.Read(t.Context(), ownerUserID, agentID, "", 100000)
	if err != nil {
		t.Fatalf("Failed to read memory: %v", err)
	}
	if len(records) != store.MaxReadLimit {
		t.Fatalf("Expected a read to be capped at %d, got %d", store.MaxReadLimit, len(records))
	}
}

// A single oversized record is refused rather than silently truncated.
func TestOversizedMemoryRecordIsRefused(t *testing.T) {
	cluster = Cluster{}
	cluster.LaunchAPI(apiPort)
	defer teardown()

	agentID := createAgent(t, "Chatty")
	memory := store.NewMemoryStore(cluster.DB)

	huge := json.RawMessage(`"` + strings.Repeat("x", store.MaxRecordBytes+1) + `"`)
	if _, err := memory.Write(t.Context(), ownerUserID, agentID, agentID, "essay", huge); err == nil {
		t.Fatal("Expected an oversized record to be refused")
	}
}

// The agent decides whether to notify, which is what makes "only tell me if
// something changed" expressible.
func TestAgentDeliversToTelegram(t *testing.T) {
	telegram, telegramServer := newRecorder(`{"ok":true}`)
	defer telegramServer.Close()

	model := newFakeModel()
	defer model.close()

	cluster = Cluster{Model: model, ToolConfig: tools.Config{
		TelegramBaseURL: telegramServer.URL,
		TelegramToken:   "test-token",
		TelegramChatID:  "4242",
	}}
	model.always(textResponse("done"))
	cluster.LaunchCluster(apiPort, ":50050", 1)
	defer teardown()

	model.queue(toolCallResponse("call-1", "send_telegram",
		`{"text":"Good morning. Three meetings today."}`))
	model.queue(textResponse("Sent the briefing."))

	agentID := agentUsing(t, model, "Brief", []string{"send_telegram"}, nil)
	runID := triggerRun(t, agentID)
	waitForRunStatus(t, runID, "succeeded")

	sent := telegram.received()
	if len(sent) != 1 {
		t.Fatalf("Expected one Telegram message, got %d", len(sent))
	}
	if !strings.Contains(sent[0], "Three meetings today.") {
		t.Errorf("Expected the agent's text to be delivered, got %s", sent[0])
	}
	if !strings.Contains(sent[0], `"chat_id":"4242"`) {
		t.Errorf("Expected the configured chat to be used, got %s", sent[0])
	}
	if !strings.Contains(telegram.requestedPaths()[0], "/bottest-token/sendMessage") {
		t.Errorf("Expected the bot token in the path, got %s", telegram.requestedPaths()[0])
	}
}

// Digests routinely exceed Telegram's 4096-character limit, so they are split
// rather than truncated.
func TestLongTelegramMessageIsSplit(t *testing.T) {
	telegram, telegramServer := newRecorder(`{"ok":true}`)
	defer telegramServer.Close()

	model := newFakeModel()
	defer model.close()

	cluster = Cluster{Model: model, ToolConfig: tools.Config{
		TelegramBaseURL: telegramServer.URL,
		TelegramToken:   "test-token",
		TelegramChatID:  "4242",
	}}
	model.always(textResponse("done"))
	cluster.LaunchCluster(apiPort, ":50050", 1)
	defer teardown()

	long := strings.Repeat("A very long line of digest text.\n", 400)
	arguments, _ := json.Marshal(map[string]string{"text": long})
	model.queue(toolCallResponse("call-1", "send_telegram", string(arguments)))
	model.queue(textResponse("Sent."))

	agentID := agentUsing(t, model, "Wordy", []string{"send_telegram"}, nil)
	runID := triggerRun(t, agentID)
	waitForRunStatus(t, runID, "succeeded")

	sent := telegram.received()
	if len(sent) < 2 {
		t.Fatalf("Expected the message to be split, got %d part(s)", len(sent))
	}

	steps := listSteps(t, runID)
	if !strings.Contains(steps[1].Result, "parts") {
		t.Errorf("Expected the trace to record that it was split, got %q", steps[1].Result)
	}
}

// The webhook is the universal escape hatch: Discord, ntfy, Home Assistant.
func TestAgentPostsToAWebhook(t *testing.T) {
	hook, hookServer := newRecorder(`{}`)
	defer hookServer.Close()

	model := launchWithModel(t)
	defer teardown()

	model.queue(toolCallResponse("call-1", "send_webhook",
		`{"url":"`+hookServer.URL+`/notify","payload":{"content":"Market closed higher."}}`))
	model.queue(textResponse("Posted."))

	agentID := agentUsing(t, model, "Notifier", []string{"send_webhook"}, nil)
	runID := triggerRun(t, agentID)
	waitForRunStatus(t, runID, "succeeded")

	posted := hook.received()
	if len(posted) != 1 || !strings.Contains(posted[0], "Market closed higher.") {
		t.Fatalf("Expected the payload to be posted, got %v", posted)
	}
}

// Search is available only where a key is configured, and returns results the
// model can use.
func TestWebSearchReturnsResults(t *testing.T) {
	search, searchServer := newRecorder(`{"web":{"results":[
		{"title":"Markets rally","url":"https://example.com/a","description":"Stocks rose."},
		{"title":"Bonds steady","url":"https://example.com/b","description":"Yields flat."}]}}`)
	defer searchServer.Close()

	model := newFakeModel()
	defer model.close()

	cluster = Cluster{Model: model, ToolConfig: tools.Config{
		SearchBaseURL: searchServer.URL,
		SearchAPIKey:  "search-key",
	}}
	model.always(textResponse("done"))
	cluster.LaunchCluster(apiPort, ":50050", 1)
	defer teardown()

	model.queue(toolCallResponse("call-1", "web_search", `{"query":"market sentiment"}`))
	model.queue(textResponse("Sentiment is positive."))

	agentID := agentUsing(t, model, "Researcher", []string{"web_search"}, nil)
	runID := triggerRun(t, agentID)
	waitForRunStatus(t, runID, "succeeded")

	steps := listSteps(t, runID)
	if !strings.Contains(steps[1].Result, "Markets rally") {
		t.Fatalf("Expected search results in the trace, got %q", steps[1].Result)
	}
	if paths := search.requestedPaths(); len(paths) != 1 || !strings.Contains(paths[0], "market+sentiment") {
		t.Errorf("Expected the query to be sent to the provider, got %v", paths)
	}
}

// A tool whose credential is not configured is absent from the catalogue rather
// than listed and broken.
func TestUnconfiguredToolsAreNotOffered(t *testing.T) {
	cluster = Cluster{}
	cluster.LaunchAPI(apiPort)
	defer teardown()

	status, body := apiRequest(t, http.MethodGet, "/api/tools", nil)
	if status != http.StatusOK {
		t.Fatalf("Expected 200 listing tools, got %d: %s", status, body)
	}

	var offered []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(body, &offered); err != nil {
		t.Fatalf("Failed to decode the catalogue: %v", err)
	}

	available := map[string]bool{}
	for _, tool := range offered {
		available[tool.Name] = true
		if tool.Description == "" {
			t.Errorf("Expected %s to describe itself to the model", tool.Name)
		}
	}

	for _, expected := range []string{"http_fetch", "send_webhook", "memory_read", "memory_write"} {
		if !available[expected] {
			t.Errorf("Expected %s to be offered", expected)
		}
	}
	for _, unconfigured := range []string{"web_search", "send_telegram"} {
		if available[unconfigured] {
			t.Errorf("Expected %s to be absent without a credential", unconfigured)
		}
	}
}
