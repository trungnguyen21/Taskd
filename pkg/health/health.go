// Package health serves the probes an orchestrator needs.
//
// The coordinator and the workers speak gRPC, which Kubernetes cannot probe
// without extra machinery, so each runs a small HTTP listener alongside it.
package health

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"time"
)

// Check reports whether a service is working. A nil error means healthy.
type Check func(ctx context.Context) error

const probeTimeout = 5 * time.Second

// Server exposes liveness and readiness.
type Server struct {
	httpServer *http.Server
}

// Serve starts a health listener.
//
// Liveness answers whether the process is running at all; readiness answers
// whether it should be given work. They are separate because a worker that is
// draining is alive and must not be killed, but must not be given anything new.
func Serve(address string, live, ready Check) (*Server, error) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handle(live))
	mux.HandleFunc("GET /readyz", handle(ready))

	server := &http.Server{Addr: address, Handler: mux}

	listener, err := listen(address)
	if err != nil {
		return nil, err
	}

	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("Health server stopped: %v", err)
		}
	}()

	return &Server{httpServer: server}, nil
}

// Stop shuts the health listener down.
func (s *Server) Stop() {
	if s == nil || s.httpServer == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	s.httpServer.Shutdown(ctx)
}

func handle(check Check) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), probeTimeout)
		defer cancel()

		status := http.StatusOK
		payload := map[string]string{"status": "ok"}

		if check != nil {
			if err := check(ctx); err != nil {
				status = http.StatusServiceUnavailable
				payload = map[string]string{"status": "unavailable", "reason": err.Error()}
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(payload)
	}
}

// listen opens the health port. It is separate so that a caller can be told the
// port is unavailable rather than discovering it in a goroutine.
func listen(address string) (net.Listener, error) {
	return net.Listen("tcp", address)
}
