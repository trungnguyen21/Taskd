package tests

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

const apiPort = ":8081"

// apiRequest drives the dashboard-facing REST API the way the dashboard does.
// Tests assert on what a user could observe through it and never on the
// internals of the services behind it.
func apiRequest(t *testing.T, method, path string, body interface{}) (int, []byte) {
	t.Helper()

	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("Failed to encode request body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequest(method, "http://localhost"+apiPort+path, reader)
	if err != nil {
		t.Fatalf("Failed to build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Request to %s %s failed: %v", method, path, err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read response body: %v", err)
	}
	return resp.StatusCode, responseBody
}

func decodeAgent(t *testing.T, body []byte) map[string]interface{} {
	t.Helper()
	var agent map[string]interface{}
	if err := json.Unmarshal(body, &agent); err != nil {
		t.Fatalf("Failed to decode agent: %v (body: %s)", err, body)
	}
	return agent
}

func newAgentPayload(name string) map[string]interface{} {
	return map[string]interface{}{
		"name":          name,
		"model":         "gpt-4o-mini",
		"system_prompt": "You are a helpful scheduled agent.",
		"user_prompt":   "Summarise the day.",
		"tools":         []string{"web_search", "send_telegram"},
	}
}

func setupAPI() {
	cluster = Cluster{}
	cluster.LaunchAPI(apiPort)
}

func TestAgentLifecycle(t *testing.T) {
	setupAPI()
	defer teardown()

	// Creating an agent applies the budget defaults, so a user is protected
	// before they have thought about limits at all.
	status, body := apiRequest(t, http.MethodPost, "/api/agents", newAgentPayload("Morning brief"))
	if status != http.StatusCreated {
		t.Fatalf("Expected 201 creating an agent, got %d: %s", status, body)
	}
	created := decodeAgent(t, body)

	id, ok := created["id"].(string)
	if !ok || id == "" {
		t.Fatalf("Created agent has no id: %s", body)
	}
	if created["max_steps"].(float64) != 20 {
		t.Errorf("Expected default max_steps of 20, got %v", created["max_steps"])
	}
	if created["max_tokens"].(float64) != 100000 {
		t.Errorf("Expected default max_tokens of 100000, got %v", created["max_tokens"])
	}
	if created["max_duration_seconds"].(float64) != 600 {
		t.Errorf("Expected default max_duration_seconds of 600, got %v", created["max_duration_seconds"])
	}
	if created["context_mode"] != "fresh" {
		t.Errorf("Expected runs to start fresh by default, got %v", created["context_mode"])
	}
	if created["enabled"] != true {
		t.Errorf("Expected a new agent to be enabled, got %v", created["enabled"])
	}

	// The agent appears in the list the dashboard opens on.
	status, body = apiRequest(t, http.MethodGet, "/api/agents", nil)
	if status != http.StatusOK {
		t.Fatalf("Expected 200 listing agents, got %d: %s", status, body)
	}
	var listed []map[string]interface{}
	if err := json.Unmarshal(body, &listed); err != nil {
		t.Fatalf("Failed to decode agent list: %v", err)
	}
	if len(listed) != 1 || listed[0]["id"] != id {
		t.Fatalf("Expected the created agent in the list, got: %s", body)
	}

	// Fetching it by id returns what was stored.
	status, body = apiRequest(t, http.MethodGet, "/api/agents/"+id, nil)
	if status != http.StatusOK {
		t.Fatalf("Expected 200 fetching the agent, got %d: %s", status, body)
	}
	fetched := decodeAgent(t, body)
	if fetched["name"] != "Morning brief" {
		t.Errorf("Expected the stored name, got %v", fetched["name"])
	}
	tools, _ := fetched["tools"].([]interface{})
	if len(tools) != 2 || tools[0] != "web_search" || tools[1] != "send_telegram" {
		t.Errorf("Expected the granted tools to round-trip, got %v", fetched["tools"])
	}

	// A partial update changes only what it names: disabling the agent must not
	// clear the prompt it was created with.
	status, body = apiRequest(t, http.MethodPatch, "/api/agents/"+id, map[string]interface{}{
		"name":    "Morning brief v2",
		"enabled": false,
	})
	if status != http.StatusOK {
		t.Fatalf("Expected 200 updating the agent, got %d: %s", status, body)
	}
	updated := decodeAgent(t, body)
	if updated["name"] != "Morning brief v2" {
		t.Errorf("Expected the updated name, got %v", updated["name"])
	}
	if updated["enabled"] != false {
		t.Errorf("Expected the agent to be disabled, got %v", updated["enabled"])
	}
	if updated["system_prompt"] != "You are a helpful scheduled agent." {
		t.Errorf("Expected the untouched prompt to survive the patch, got %v", updated["system_prompt"])
	}
	if updated["model"] != "gpt-4o-mini" {
		t.Errorf("Expected the untouched model to survive the patch, got %v", updated["model"])
	}

	// Deleting removes it.
	status, body = apiRequest(t, http.MethodDelete, "/api/agents/"+id, nil)
	if status != http.StatusNoContent {
		t.Fatalf("Expected 204 deleting the agent, got %d: %s", status, body)
	}

	status, _ = apiRequest(t, http.MethodGet, "/api/agents/"+id, nil)
	if status != http.StatusNotFound {
		t.Fatalf("Expected 404 fetching a deleted agent, got %d", status)
	}

	status, _ = apiRequest(t, http.MethodDelete, "/api/agents/"+id, nil)
	if status != http.StatusNotFound {
		t.Fatalf("Expected 404 deleting an agent twice, got %d", status)
	}
}

func TestAgentValidation(t *testing.T) {
	setupAPI()
	defer teardown()

	cases := []struct {
		name    string
		payload map[string]interface{}
	}{
		{"missing name", map[string]interface{}{"model": "gpt-4o-mini"}},
		{"missing model", map[string]interface{}{"name": "No model"}},
		{"unknown tool", map[string]interface{}{
			"name": "Bad tool", "model": "gpt-4o-mini", "tools": []string{"send_carrier_pigeon"}}},
		{"steps above the ceiling", map[string]interface{}{
			"name": "Runaway", "model": "gpt-4o-mini", "max_steps": 10000}},
		{"tokens above the ceiling", map[string]interface{}{
			"name": "Expensive", "model": "gpt-4o-mini", "max_tokens": 999999999}},
		{"duration above the ceiling", map[string]interface{}{
			"name": "Forever", "model": "gpt-4o-mini", "max_duration_seconds": 86400}},
		{"last_n without a count", map[string]interface{}{
			"name": "Contextual", "model": "gpt-4o-mini", "context_mode": "last_n"}},
		{"unknown context mode", map[string]interface{}{
			"name": "Confused", "model": "gpt-4o-mini", "context_mode": "everything"}},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			status, body := apiRequest(t, http.MethodPost, "/api/agents", testCase.payload)
			if status != http.StatusBadRequest {
				t.Fatalf("Expected 400 for %s, got %d: %s", testCase.name, status, body)
			}
		})
	}

	status, body := apiRequest(t, http.MethodGet, "/api/agents", nil)
	if status != http.StatusOK {
		t.Fatalf("Expected 200 listing agents, got %d", status)
	}
	var listed []map[string]interface{}
	json.Unmarshal(body, &listed)
	if len(listed) != 0 {
		t.Fatalf("Expected no agents to have been stored, got %d", len(listed))
	}
}

func TestToolCatalogIsExposed(t *testing.T) {
	setupAPI()
	defer teardown()

	status, body := apiRequest(t, http.MethodGet, "/api/tools", nil)
	if status != http.StatusOK {
		t.Fatalf("Expected 200 listing tools, got %d: %s", status, body)
	}

	var tools []string
	if err := json.Unmarshal(body, &tools); err != nil {
		t.Fatalf("Failed to decode the tool catalog: %v", err)
	}

	expected := []string{"web_search", "http_fetch", "memory_read", "memory_write",
		"send_telegram", "send_webhook"}
	if len(tools) != len(expected) {
		t.Fatalf("Expected %d tools in the catalog, got %v", len(expected), tools)
	}
	for i, name := range expected {
		if tools[i] != name {
			t.Errorf("Expected tool %d to be %s, got %s", i, name, tools[i])
		}
	}
}

// An operator upgrading an existing install must not have to apply SQL by hand,
// so starting against a database that already has the schema has to work.
func TestMigrationsRunOnEveryStart(t *testing.T) {
	setupAPI()
	defer teardown()

	status, body := apiRequest(t, http.MethodPost, "/api/agents", newAgentPayload("Survivor"))
	if status != http.StatusCreated {
		t.Fatalf("Expected 201 creating an agent, got %d: %s", status, body)
	}
	id := decodeAgent(t, body)["id"].(string)

	cluster.StopAPI()
	cluster.StartAPI(apiPort)

	status, body = apiRequest(t, http.MethodGet, "/api/agents/"+id, nil)
	if status != http.StatusOK {
		t.Fatalf("Expected the agent to survive a restart, got %d: %s", status, body)
	}
	if name := decodeAgent(t, body)["name"]; name != "Survivor" {
		t.Errorf("Expected the stored agent after restart, got %v", name)
	}
}
