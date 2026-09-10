package tests

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func createAgent(t *testing.T, name string) string {
	t.Helper()
	status, body := apiRequest(t, http.MethodPost, "/api/agents", newAgentPayload(name))
	if status != http.StatusCreated {
		t.Fatalf("Expected 201 creating an agent, got %d: %s", status, body)
	}
	return decodeAgent(t, body)["id"].(string)
}

func setSchedule(t *testing.T, agentID, expression, timezone string) map[string]interface{} {
	t.Helper()
	status, body := apiRequest(t, http.MethodPut, "/api/agents/"+agentID+"/schedule",
		map[string]interface{}{"cron_expression": expression, "timezone": timezone})
	if status != http.StatusOK {
		t.Fatalf("Expected 200 setting a schedule, got %d: %s", status, body)
	}
	return decodeAgent(t, body)
}

func listRuns(t *testing.T, agentID string) []map[string]interface{} {
	t.Helper()
	status, body := apiRequest(t, http.MethodGet, "/api/agents/"+agentID+"/runs", nil)
	if status != http.StatusOK {
		t.Fatalf("Expected 200 listing runs, got %d: %s", status, body)
	}
	var runs []map[string]interface{}
	if err := json.Unmarshal(body, &runs); err != nil {
		t.Fatalf("Failed to decode runs: %v (body: %s)", err, body)
	}
	return runs
}

// materialize advances the shared clock and expands whatever is now due.
func materialize(t *testing.T, at time.Time) {
	t.Helper()
	cluster.Clock.Set(at)
	if _, err := cluster.Materializer.RunOnce(context.Background()); err != nil {
		t.Fatalf("Materializing schedules failed: %v", err)
	}
}

func mustLoad(t *testing.T, timezone string) *time.Location {
	t.Helper()
	location, err := time.LoadLocation(timezone)
	if err != nil {
		t.Fatalf("Timezone database unavailable: %v", err)
	}
	return location
}

func TestScheduleFiresAtTheUsersWallClockTime(t *testing.T) {
	losAngeles := mustLoad(t, "America/Los_Angeles")
	cluster = Cluster{Clock: clockAt(time.Date(2026, time.June, 1, 6, 0, 0, 0, losAngeles))}
	cluster.LaunchAPI(apiPort)
	defer teardown()

	agentID := createAgent(t, "Morning brief")
	setSchedule(t, agentID, "0 7 * * *", "America/Los_Angeles")

	// A minute before the fire time nothing has happened yet.
	materialize(t, time.Date(2026, time.June, 1, 6, 59, 0, 0, losAngeles))
	if runs := listRuns(t, agentID); len(runs) != 0 {
		t.Fatalf("Expected no runs before the fire time, got %d", len(runs))
	}

	// At 7am local the run exists and is queued for execution.
	materialize(t, time.Date(2026, time.June, 1, 7, 0, 0, 0, losAngeles))
	runs := listRuns(t, agentID)
	if len(runs) != 1 {
		t.Fatalf("Expected one run at the fire time, got %d", len(runs))
	}
	if runs[0]["status"] != "pending" {
		t.Errorf("Expected the run to be pending, got %v", runs[0]["status"])
	}
	if runs[0]["trigger"] != "schedule" {
		t.Errorf("Expected a schedule-triggered run, got %v", runs[0]["trigger"])
	}

	// Materializing again at the same instant must not produce a second run.
	materialize(t, time.Date(2026, time.June, 1, 7, 0, 0, 0, losAngeles))
	if runs := listRuns(t, agentID); len(runs) != 1 {
		t.Fatalf("Expected the schedule to fire once, got %d runs", len(runs))
	}

	// The following day fires again.
	materialize(t, time.Date(2026, time.June, 2, 7, 0, 0, 0, losAngeles))
	if runs := listRuns(t, agentID); len(runs) != 2 {
		t.Fatalf("Expected a run on each day, got %d runs", len(runs))
	}
}

// A fire time that passed while nothing was running is recorded rather than
// executed late, and an outage leaves one record instead of one per occurrence.
func TestMissedFireTimesAreRecordedNotExecuted(t *testing.T) {
	losAngeles := mustLoad(t, "America/Los_Angeles")
	cluster = Cluster{Clock: clockAt(time.Date(2026, time.June, 1, 6, 0, 0, 0, losAngeles))}
	cluster.LaunchAPI(apiPort)
	defer teardown()

	agentID := createAgent(t, "Hourly digest")
	setSchedule(t, agentID, "0 * * * *", "America/Los_Angeles")

	// Nothing runs for most of a day, then the system comes back.
	materialize(t, time.Date(2026, time.June, 2, 5, 30, 0, 0, losAngeles))

	runs := listRuns(t, agentID)
	if len(runs) != 1 {
		t.Fatalf("Expected an outage to leave a single missed record, got %d runs", len(runs))
	}
	if runs[0]["status"] != "missed" {
		t.Errorf("Expected the stale fire time to be recorded as missed, got %v", runs[0]["status"])
	}

	// The schedule resumes normally afterwards.
	materialize(t, time.Date(2026, time.June, 2, 6, 0, 0, 0, losAngeles))
	runs = listRuns(t, agentID)
	if len(runs) != 2 {
		t.Fatalf("Expected the schedule to resume, got %d runs", len(runs))
	}
	if runs[0]["status"] != "pending" {
		t.Errorf("Expected the fresh fire time to be pending, got %v", runs[0]["status"])
	}
}

// 02:30 does not exist on the spring transition day, so the schedule is not
// fired that day at all.
func TestScheduleSkipsTheHourLostToDaylightSaving(t *testing.T) {
	losAngeles := mustLoad(t, "America/Los_Angeles")
	cluster = Cluster{Clock: clockAt(time.Date(2026, time.March, 7, 1, 0, 0, 0, losAngeles))}
	cluster.LaunchAPI(apiPort)
	defer teardown()

	agentID := createAgent(t, "Overnight job")
	setSchedule(t, agentID, "30 2 * * *", "America/Los_Angeles")

	materialize(t, time.Date(2026, time.March, 7, 2, 30, 0, 0, losAngeles))
	if runs := listRuns(t, agentID); len(runs) != 1 {
		t.Fatalf("Expected the 7 March run, got %d", len(runs))
	}

	// Through the whole of 8 March, when 02:30 never happens.
	materialize(t, time.Date(2026, time.March, 8, 23, 59, 0, 0, losAngeles))
	runs := listRuns(t, agentID)
	if len(runs) != 1 {
		t.Fatalf("Expected no run on 8 March - 02:30 does not exist - got %d runs", len(runs))
	}

	materialize(t, time.Date(2026, time.March, 9, 2, 30, 0, 0, losAngeles))
	if runs := listRuns(t, agentID); len(runs) != 2 {
		t.Fatalf("Expected the schedule to resume on 9 March, got %d runs", len(runs))
	}
}

// 01:30 happens twice on the autumn transition day. Both instants are real and
// the second is genuinely later, so without a wall-clock guard the agent would
// run twice - sending twice and writing to memory twice.
func TestScheduleFiresOnceInTheRepeatedDaylightSavingHour(t *testing.T) {
	losAngeles := mustLoad(t, "America/Los_Angeles")
	cluster = Cluster{Clock: clockAt(time.Date(2026, time.October, 31, 23, 0, 0, 0, losAngeles))}
	cluster.LaunchAPI(apiPort)
	defer teardown()

	agentID := createAgent(t, "Nightly job")
	setSchedule(t, agentID, "30 1 * * *", "America/Los_Angeles")

	// Step through the repeated hour instant by instant, as the real clock does.
	for _, at := range []time.Time{
		time.Date(2026, time.November, 1, 8, 30, 0, 0, time.UTC), // 01:30 PDT
		time.Date(2026, time.November, 1, 9, 0, 0, 0, time.UTC),  // 01:00 PST
		time.Date(2026, time.November, 1, 9, 30, 0, 0, time.UTC), // 01:30 PST
		time.Date(2026, time.November, 1, 10, 0, 0, 0, time.UTC), // 02:00 PST
	} {
		materialize(t, at)
	}

	runs := listRuns(t, agentID)
	if len(runs) != 1 {
		t.Fatalf("Expected one run on the day the hour repeats, got %d", len(runs))
	}

	materialize(t, time.Date(2026, time.November, 2, 1, 30, 0, 0, losAngeles))
	if runs := listRuns(t, agentID); len(runs) != 2 {
		t.Fatalf("Expected the schedule to resume the next day, got %d runs", len(runs))
	}
}

func TestScheduleValidationAndPreview(t *testing.T) {
	cluster = Cluster{}
	cluster.LaunchAPI(apiPort)
	defer teardown()

	agentID := createAgent(t, "Configurable")

	cases := []struct {
		name    string
		payload map[string]interface{}
	}{
		{"unparseable expression", map[string]interface{}{
			"cron_expression": "every thursday", "timezone": "UTC"}},
		{"unknown timezone", map[string]interface{}{
			"cron_expression": "0 7 * * *", "timezone": "Mars/Olympus_Mons"}},
		{"missing timezone", map[string]interface{}{
			"cron_expression": "0 7 * * *"}},
		{"finer than the minimum granularity", map[string]interface{}{
			"cron_expression": "* * * * *", "timezone": "UTC"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			status, body := apiRequest(t, http.MethodPut,
				"/api/agents/"+agentID+"/schedule", testCase.payload)
			if status != http.StatusBadRequest {
				t.Fatalf("Expected 400 for %s, got %d: %s", testCase.name, status, body)
			}
		})
	}

	// The picker shows upcoming fire times so a schedule can be confirmed
	// before it is saved.
	status, body := apiRequest(t, http.MethodPost, "/api/schedules/preview",
		map[string]interface{}{"cron_expression": "0 7 * * *", "timezone": "America/Los_Angeles"})
	if status != http.StatusOK {
		t.Fatalf("Expected 200 previewing a schedule, got %d: %s", status, body)
	}
	var preview struct {
		FireTimes []time.Time `json:"fire_times"`
	}
	if err := json.Unmarshal(body, &preview); err != nil {
		t.Fatalf("Failed to decode the preview: %v", err)
	}
	if len(preview.FireTimes) != 5 {
		t.Fatalf("Expected five upcoming fire times, got %d", len(preview.FireTimes))
	}
	losAngeles := mustLoad(t, "America/Los_Angeles")
	for _, fireTime := range preview.FireTimes {
		if hour := fireTime.In(losAngeles).Hour(); hour != 7 {
			t.Errorf("Expected every preview time to be 7am local, got %s", fireTime.In(losAngeles))
		}
	}
}

func TestScheduleLifecycle(t *testing.T) {
	cluster = Cluster{}
	cluster.LaunchAPI(apiPort)
	defer teardown()

	agentID := createAgent(t, "Scheduled")

	status, _ := apiRequest(t, http.MethodGet, "/api/agents/"+agentID+"/schedule", nil)
	if status != http.StatusNotFound {
		t.Fatalf("Expected 404 before a schedule exists, got %d", status)
	}

	setSchedule(t, agentID, "0 7 * * *", "America/Los_Angeles")

	status, body := apiRequest(t, http.MethodGet, "/api/agents/"+agentID+"/schedule", nil)
	if status != http.StatusOK {
		t.Fatalf("Expected 200 fetching the schedule, got %d: %s", status, body)
	}
	stored := decodeAgent(t, body)
	if stored["cron_expression"] != "0 7 * * *" || stored["timezone"] != "America/Los_Angeles" {
		t.Errorf("Expected the stored schedule to round-trip, got %s", body)
	}
	if stored["next_fire_at"] == nil {
		t.Error("Expected a computed next fire time")
	}

	// Replacing the schedule keeps one per agent rather than accumulating.
	replaced := setSchedule(t, agentID, "0 18 * * *", "Europe/London")
	if replaced["cron_expression"] != "0 18 * * *" || replaced["timezone"] != "Europe/London" {
		t.Errorf("Expected the schedule to be replaced, got %v", replaced)
	}

	status, _ = apiRequest(t, http.MethodDelete, "/api/agents/"+agentID+"/schedule", nil)
	if status != http.StatusNoContent {
		t.Fatalf("Expected 204 deleting the schedule, got %d", status)
	}
	status, _ = apiRequest(t, http.MethodGet, "/api/agents/"+agentID+"/schedule", nil)
	if status != http.StatusNotFound {
		t.Fatalf("Expected 404 after deleting the schedule, got %d", status)
	}
}

// A disabled agent is paused, so its schedule stops producing runs without
// being deleted.
func TestDisabledAgentDoesNotFire(t *testing.T) {
	losAngeles := mustLoad(t, "America/Los_Angeles")
	cluster = Cluster{Clock: clockAt(time.Date(2026, time.June, 1, 6, 0, 0, 0, losAngeles))}
	cluster.LaunchAPI(apiPort)
	defer teardown()

	agentID := createAgent(t, "Paused")
	setSchedule(t, agentID, "0 7 * * *", "America/Los_Angeles")

	status, body := apiRequest(t, http.MethodPatch, "/api/agents/"+agentID,
		map[string]interface{}{"enabled": false})
	if status != http.StatusOK {
		t.Fatalf("Expected 200 disabling the agent, got %d: %s", status, body)
	}

	materialize(t, time.Date(2026, time.June, 1, 7, 0, 0, 0, losAngeles))
	if runs := listRuns(t, agentID); len(runs) != 0 {
		t.Fatalf("Expected a disabled agent not to fire, got %d runs", len(runs))
	}
}

// Triggering by hand queues a run immediately, so an agent can be tested
// without waiting for its schedule.
func TestManualTriggerQueuesARun(t *testing.T) {
	cluster = Cluster{}
	cluster.LaunchAPI(apiPort)
	defer teardown()

	agentID := createAgent(t, "On demand")

	status, body := apiRequest(t, http.MethodPost, "/api/agents/"+agentID+"/runs", nil)
	if status != http.StatusCreated {
		t.Fatalf("Expected 201 triggering a run, got %d: %s", status, body)
	}
	run := decodeAgent(t, body)
	if run["trigger"] != "manual" {
		t.Errorf("Expected a manually triggered run, got %v", run["trigger"])
	}
	if run["status"] != "pending" {
		t.Errorf("Expected the run to be pending, got %v", run["status"])
	}

	status, body = apiRequest(t, http.MethodGet, "/api/runs/"+run["id"].(string), nil)
	if status != http.StatusOK {
		t.Fatalf("Expected 200 fetching the run, got %d: %s", status, body)
	}
	if decodeAgent(t, body)["agent_id"] != agentID {
		t.Errorf("Expected the run to belong to the agent")
	}

	status, _ = apiRequest(t, http.MethodPost, "/api/agents/"+agentID+"-missing/runs", nil)
	if status != http.StatusNotFound {
		t.Fatalf("Expected 404 triggering a run for an unknown agent, got %d", status)
	}
}
