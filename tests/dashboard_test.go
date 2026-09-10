package tests

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestDashboardIsServedByTheAPIService pins the promise that one command yields
// a working URL: the bundle comes out of the same process that answers the API,
// with no second container and no build step in front of it.
func TestDashboardIsServedByTheAPIService(t *testing.T) {
	setupAPI()
	defer teardown()

	status, body := apiRequest(t, http.MethodGet, "/", nil)
	if status != http.StatusOK {
		t.Fatalf("Expected 200 for the dashboard, got %d", status)
	}
	if !strings.Contains(string(body), "<title>Taskd</title>") {
		t.Errorf("The dashboard document was not served: %s", truncate(body))
	}

	// The document is useless without the modules it loads.
	for _, asset := range []string{"/app.js", "/app.css", "/contract.json", "/screens/agents.js"} {
		status, body := apiRequest(t, http.MethodGet, asset, nil)
		if status != http.StatusOK {
			t.Errorf("Expected 200 for %s, got %d: %s", asset, status, truncate(body))
		}
	}

	// Screens are addressed by fragment, so a path that is not a file is a
	// stale bookmark and lands on the dashboard rather than on a bare 404.
	status, body = apiRequest(t, http.MethodGet, "/agents/some-old-link", nil)
	if status != http.StatusOK || !strings.Contains(string(body), "<title>Taskd</title>") {
		t.Errorf("Expected an unknown path to serve the dashboard, got %d: %s", status, truncate(body))
	}
}

// TestDashboardAssetsDoNotOpenTheAPI guards the mount added for the bundle.
// Serving files from the root must not turn the closed API into an open one.
func TestDashboardAssetsDoNotOpenTheAPI(t *testing.T) {
	setupAPI()
	defer teardown()

	signedIn := cluster.Session
	cluster.Session = nil
	defer func() { cluster.Session = signedIn }()

	if status, _ := apiRequest(t, http.MethodGet, "/", nil); status != http.StatusOK {
		t.Errorf("Expected the dashboard to load before signing in, got %d", status)
	}
	if status, body := apiRequest(t, http.MethodGet, "/api/agents", nil); status != http.StatusUnauthorized {
		t.Errorf("Expected 401 for the API without a session, got %d: %s", status, truncate(body))
	}
}

// TestAgentListAnswersTheHomeScreenInOneRequest covers the question the home
// screen exists to answer - whether anything is broken - which cannot be
// answered from the agent rows alone. Asking per agent would be a request per
// row, so the schedule and the last run travel with the agent.
func TestAgentListAnswersTheHomeScreenInOneRequest(t *testing.T) {
	setupAPI()
	defer teardown()

	scheduled := createAgent(t, "Morning brief")
	setSchedule(t, scheduled, "0 7 * * *", "Europe/London")

	// A second agent with no schedule at all, so the list is exercised with
	// both shapes rather than only the populated one.
	unscheduled := createAgent(t, "On demand")

	status, body := apiRequest(t, http.MethodPost, "/api/agents/"+scheduled+"/runs", nil)
	if status != http.StatusCreated {
		t.Fatalf("Expected 201 triggering a run, got %d: %s", status, body)
	}

	rows := listAgentOverviews(t)
	if len(rows) != 2 {
		t.Fatalf("Expected both agents in the list, got %d", len(rows))
	}

	byID := map[string]map[string]interface{}{}
	for _, row := range rows {
		byID[row["id"].(string)] = row
	}

	withSchedule := byID[scheduled]
	schedule, ok := withSchedule["schedule"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected the schedule on the agent's list row, got %v", withSchedule["schedule"])
	}
	if schedule["cron_expression"] != "0 7 * * *" || schedule["timezone"] != "Europe/London" {
		t.Errorf("The list row carries the wrong schedule: %v", schedule)
	}

	lastRun, ok := withSchedule["last_run"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected the last run on the agent's list row, got %v", withSchedule["last_run"])
	}
	if lastRun["status"] != "pending" || lastRun["trigger"] != "manual" {
		t.Errorf("The list row carries the wrong run: %v", lastRun)
	}

	// An agent that has neither says so, rather than being absent or partial.
	if byID[unscheduled]["schedule"] != nil {
		t.Errorf("Expected no schedule for the unscheduled agent, got %v", byID[unscheduled]["schedule"])
	}
	if byID[unscheduled]["last_run"] != nil {
		t.Errorf("Expected no last run for an agent that never ran, got %v", byID[unscheduled]["last_run"])
	}
	if byID[unscheduled]["name"] != "On demand" {
		t.Errorf("The list row lost the agent's own fields: %v", byID[unscheduled])
	}
}

func listAgentOverviews(t *testing.T) []map[string]interface{} {
	t.Helper()
	status, body := apiRequest(t, http.MethodGet, "/api/agents", nil)
	if status != http.StatusOK {
		t.Fatalf("Expected 200 listing agents, got %d: %s", status, body)
	}
	var rows []map[string]interface{}
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("Failed to decode the agent list: %v (body: %s)", err, body)
	}
	return rows
}

func truncate(body []byte) string {
	if len(body) > 200 {
		return string(body[:200]) + "…"
	}
	return string(body)
}
