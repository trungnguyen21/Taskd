package tests

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/JyotinderSingh/task-queue/pkg/common"
	"github.com/JyotinderSingh/task-queue/pkg/reaper"
)

var cluster Cluster

func teardown() {
	cluster.StopCluster()
}

func TestMain(m *testing.M) {
	code := m.Run()
	stopSharedDatabase()
	os.Exit(code)
}

// getRun reads one run through the API the dashboard uses.
func getRun(t *testing.T, runID string) map[string]interface{} {
	t.Helper()
	status, body := apiRequest(t, http.MethodGet, "/api/runs/"+runID, nil)
	if status != http.StatusOK {
		t.Fatalf("Expected 200 fetching run %s, got %d: %s", runID, status, body)
	}
	return decodeAgent(t, body)
}

func triggerRun(t *testing.T, agentID string) string {
	t.Helper()
	status, body := apiRequest(t, http.MethodPost, "/api/agents/"+agentID+"/runs", nil)
	if status != http.StatusCreated {
		t.Fatalf("Expected 201 triggering a run, got %d: %s", status, body)
	}
	return decodeAgent(t, body)["id"].(string)
}

func waitForRunStatus(t *testing.T, runID, want string) map[string]interface{} {
	t.Helper()
	var last map[string]interface{}
	err := WaitForCondition(func() bool {
		last = getRun(t, runID)
		return last["status"] == want
	}, 30*time.Second, 250*time.Millisecond)
	if err != nil {
		t.Fatalf("Run %s never reached %q; last status was %v", runID, want, last["status"])
	}
	return last
}

// A run reaches a worker, executes and reports back, with the whole journey
// visible through the API.
func TestRunIsDispatchedExecutedAndReported(t *testing.T) {
	cluster = Cluster{}
	cluster.LaunchCluster(apiPort, coordinatorPort, 2)
	defer teardown()

	agentID := createAgent(t, "Dispatched")
	runID := triggerRun(t, agentID)

	run := waitForRunStatus(t, runID, "succeeded")

	if run["picked_at"] == nil {
		t.Error("Expected the run to record when the coordinator picked it up")
	}
	if run["started_at"] == nil {
		t.Error("Expected the run to record when the worker started it")
	}
	if run["finished_at"] == nil {
		t.Error("Expected the run to record when it finished")
	}
}

// Runs are spread over the pool, and all of them complete.
func TestRunsAreDistributedAcrossWorkers(t *testing.T) {
	cluster = Cluster{}
	cluster.LaunchCluster(apiPort, coordinatorPort, 3)
	defer teardown()

	agentID := createAgent(t, "Busy")

	runIDs := make([]string, 0, 6)
	for i := 0; i < 6; i++ {
		runIDs = append(runIDs, triggerRun(t, agentID))
	}

	for _, runID := range runIDs {
		waitForRunStatus(t, runID, "succeeded")
	}
}

// With no worker to take it, a run waits rather than being marked as picked up
// and then dropped.
func TestRunStaysPendingWhileNoWorkersAreAvailable(t *testing.T) {
	cluster = Cluster{}
	cluster.LaunchCluster(apiPort, coordinatorPort, 1)
	defer teardown()

	agentID := createAgent(t, "Unserviced")

	for _, worker := range cluster.workers {
		if err := worker.Stop(); err != nil {
			t.Fatalf("Failed to stop worker: %v", err)
		}
	}
	// Wait for the coordinator to notice the worker is gone.
	if err := WaitForCondition(func() bool {
		cluster.coordinator.WorkerPoolKeysMutex.RLock()
		defer cluster.coordinator.WorkerPoolKeysMutex.RUnlock()
		return len(cluster.coordinator.WorkerPoolKeys) == 0
	}, 30*time.Second, common.DefaultHeartbeat); err != nil {
		t.Fatalf("Coordinator did not release the stopped worker: %v", err)
	}

	runID := triggerRun(t, agentID)

	// Give the coordinator several scans to prove it is not dispatching.
	time.Sleep(3 * time.Second)
	if status := getRun(t, runID)["status"]; status != "pending" {
		t.Fatalf("Expected the run to remain pending with no workers, got %v", status)
	}
}

// A worker that stops renewing its lease loses the run, which is failed rather
// than left running forever. Inserting the abandoned run directly stands in for
// the worker dying: on Kubernetes that is a rolling deploy, not an exception.
func TestRunWithAnExpiredLeaseIsFailed(t *testing.T) {
	cluster = Cluster{Clock: clockAt(time.Now())}
	cluster.LaunchAPI(apiPort)
	defer teardown()

	runID := triggerRun(t, createAgent(t, "Abandoned"))

	abandonRun(t, runID, cluster.Clock.Now().Add(-time.Minute))

	reaped, err := cluster.Reaper.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("Reaping failed: %v", err)
	}
	if reaped != 1 {
		t.Fatalf("Expected one run to be reaped, got %d", reaped)
	}

	run := getRun(t, runID)
	if run["status"] != "failed" {
		t.Fatalf("Expected the abandoned run to be failed, got %v", run["status"])
	}
	if run["error"] == "" {
		t.Error("Expected the failure to record why the run was abandoned")
	}
	if run["finished_at"] == nil {
		t.Error("Expected the reaped run to record when it ended")
	}
}

// A run whose lease is still being renewed is left alone.
func TestRunWithALiveLeaseIsNotReaped(t *testing.T) {
	cluster = Cluster{Clock: clockAt(time.Now())}
	cluster.LaunchAPI(apiPort)
	defer teardown()

	runID := triggerRun(t, createAgent(t, "Healthy"))

	abandonRun(t, runID, cluster.Clock.Now().Add(reaper.LeaseTTL))

	reaped, err := cluster.Reaper.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("Reaping failed: %v", err)
	}
	if reaped != 0 {
		t.Fatalf("Expected a live lease to survive reaping, got %d reaped", reaped)
	}

	if status := getRun(t, runID)["status"]; status != "running" {
		t.Fatalf("Expected the run to still be running, got %v", status)
	}
}

// The coordinator forgets workers that stop sending heartbeats.
func TestCoordinatorReleasesInactiveWorkers(t *testing.T) {
	cluster = Cluster{}
	cluster.LaunchCluster(apiPort, coordinatorPort, 2)
	defer teardown()

	if err := cluster.workers[0].Stop(); err != nil {
		t.Fatalf("Failed to stop worker: %v", err)
	}

	if err := WaitForCondition(func() bool {
		cluster.coordinator.WorkerPoolKeysMutex.RLock()
		defer cluster.coordinator.WorkerPoolKeysMutex.RUnlock()
		return len(cluster.coordinator.WorkerPoolKeys) == 1
	}, 30*time.Second, common.DefaultHeartbeat); err != nil {
		t.Fatalf("Coordinator did not release the inactive worker: %v", err)
	}

	// The surviving worker still serves runs.
	agentID := createAgent(t, "Survivor")
	waitForRunStatus(t, triggerRun(t, agentID), "succeeded")
}

// abandonRun puts a run into the state a worker leaves behind when it claims a
// run and then dies: still running, with a lease that nothing is renewing.
func abandonRun(t *testing.T, runID string, leaseExpiresAt time.Time) {
	t.Helper()

	_, err := cluster.DB.Exec(context.Background(),
		`UPDATE runs SET status = 'running', picked_at = $2, started_at = $2,
			lease_expires_at = $3 WHERE id = $1`,
		runID, cluster.Clock.Now(), leaseExpiresAt)
	if err != nil {
		t.Fatalf("Failed to abandon run %s: %v", runID, err)
	}
}
