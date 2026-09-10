package health

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"testing"
)

func freeAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Could not find a free port: %v", err)
	}
	defer listener.Close()
	return fmt.Sprintf("127.0.0.1:%d", listener.Addr().(*net.TCPAddr).Port)
}

func get(t *testing.T, address, path string) int {
	t.Helper()
	response, err := http.Get("http://" + address + path)
	if err != nil {
		t.Fatalf("Probe %s failed: %v", path, err)
	}
	defer response.Body.Close()
	return response.StatusCode
}

// Liveness and readiness answer separately: a service that is draining is alive
// and must not be killed, but must not be given new work either.
func TestLivenessAndReadinessAreSeparate(t *testing.T) {
	draining := false

	address := freeAddress(t)
	server, err := Serve(address,
		func(context.Context) error { return nil },
		func(context.Context) error {
			if draining {
				return errors.New("draining")
			}
			return nil
		})
	if err != nil {
		t.Fatalf("Could not start the health server: %v", err)
	}
	defer server.Stop()

	if status := get(t, address, "/healthz"); status != http.StatusOK {
		t.Errorf("Expected liveness to be 200, got %d", status)
	}
	if status := get(t, address, "/readyz"); status != http.StatusOK {
		t.Errorf("Expected readiness to be 200, got %d", status)
	}

	draining = true

	if status := get(t, address, "/healthz"); status != http.StatusOK {
		t.Errorf("Expected a draining service to stay alive, got %d", status)
	}
	if status := get(t, address, "/readyz"); status != http.StatusServiceUnavailable {
		t.Errorf("Expected a draining service not to be ready, got %d", status)
	}
}

// A failing check is reported as unavailable rather than as a crash.
func TestFailingCheckIsReportedAsUnavailable(t *testing.T) {
	address := freeAddress(t)
	server, err := Serve(address,
		func(context.Context) error { return errors.New("database unreachable") },
		nil)
	if err != nil {
		t.Fatalf("Could not start the health server: %v", err)
	}
	defer server.Stop()

	if status := get(t, address, "/healthz"); status != http.StatusServiceUnavailable {
		t.Errorf("Expected 503 when the check fails, got %d", status)
	}
}

// A port already in use is reported to the caller rather than lost in a
// goroutine.
func TestUnavailablePortIsReported(t *testing.T) {
	address := freeAddress(t)
	first, err := Serve(address, nil, nil)
	if err != nil {
		t.Fatalf("Could not start the first health server: %v", err)
	}
	defer first.Stop()

	if _, err := Serve(address, nil, nil); err == nil {
		t.Fatal("Expected starting a second server on the same port to fail")
	}
}
