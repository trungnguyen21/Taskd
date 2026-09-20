package tests

import (
	"net/http"
	"sync"
	"testing"
	"time"
)

// An orchestrator needs to know whether each service is alive, and the
// coordinator speaks gRPC, which Kubernetes cannot probe on its own.
func TestCoordinatorServesProbes(t *testing.T) {
	cluster = Cluster{CoordinatorHealthPort: ":18090"}
	cluster.LaunchCluster(apiPort, coordinatorPort, 1)
	defer teardown()

	for _, path := range []string{"/healthz", "/readyz"} {
		response, err := http.Get("http://localhost:18090" + path)
		if err != nil {
			t.Fatalf("Probe %s failed: %v", path, err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Errorf("Expected %s to be 200, got %d", path, response.StatusCode)
		}
	}

	// The probes are open, because an orchestrator has no session.
	response, err := http.Get("http://localhost" + apiPort + "/healthz")
	if err != nil {
		t.Fatalf("API probe failed: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Errorf("Expected the API health check to be open, got %d", response.StatusCode)
	}
}

// A worker asked to shut down finishes the run it already accepted. On
// Kubernetes this happens on every rolling deploy, and abandoning the run would
// leave the coordinator believing it is still ours until the lease expires.
func TestWorkerFinishesItsRunWhenShuttingDown(t *testing.T) {
	model := newFakeModel()
	defer model.close()

	// The model answers slowly, so the worker is certain to be mid-run when it
	// is asked to stop.
	released := make(chan struct{})
	var once sync.Once
	model.beforeRespond = func() {
		<-released
	}
	model.always(textResponse("Finished despite the shutdown."))

	cluster = Cluster{Model: model}
	cluster.LaunchCluster(apiPort, coordinatorPort, 1)
	defer teardown()

	agentID := agentUsing(t, model, "Interrupted", nil, nil)
	runID := triggerRun(t, agentID)

	// Wait until the worker has actually started the run.
	if err := WaitForCondition(func() bool {
		return getRun(t, runID)["started_at"] != nil
	}, 30*time.Second, 100*time.Millisecond); err != nil {
		t.Fatalf("The run never started: %v", err)
	}

	// Ask the worker to stop while the run is in flight, then let the model
	// answer. Stop blocks until the run is done, so it is called concurrently.
	stopped := make(chan error, 1)
	go func() {
		stopped <- cluster.workers[0].Stop()
	}()

	// Give the shutdown a moment to take effect before the run can complete.
	time.Sleep(500 * time.Millisecond)
	once.Do(func() { close(released) })

	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("Stopping the worker failed: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("The worker did not finish shutting down")
	}

	run := getRun(t, runID)
	if run["status"] != "succeeded" {
		t.Fatalf("Expected the in-flight run to finish, got %v", run["status"])
	}
	if run["output"] != "Finished despite the shutdown." {
		t.Errorf("Expected the run's output to be recorded, got %v", run["output"])
	}
}

// A draining worker declines new runs, so nothing is accepted that will not be
// executed.
func TestDrainingWorkerDeclinesNewRuns(t *testing.T) {
	model := newFakeModel()
	defer model.close()
	model.always(textResponse("done"))

	cluster = Cluster{Model: model}
	cluster.LaunchCluster(apiPort, coordinatorPort, 1)
	defer teardown()

	if err := cluster.workers[0].Stop(); err != nil {
		t.Fatalf("Stopping the worker failed: %v", err)
	}

	agentID := agentUsing(t, model, "Late", nil, nil)
	runID := triggerRun(t, agentID)

	// Several scans pass without the run being taken.
	time.Sleep(2 * time.Second)
	if status := getRun(t, runID)["status"]; status != "pending" {
		t.Fatalf("Expected a draining worker not to take the run, got %v", status)
	}
}
