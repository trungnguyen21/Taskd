package scheduler

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/JyotinderSingh/task-queue/pkg/clock"
	"github.com/JyotinderSingh/task-queue/pkg/common"
	"github.com/JyotinderSingh/task-queue/pkg/dashboard"
	"github.com/JyotinderSingh/task-queue/pkg/db"
	"github.com/JyotinderSingh/task-queue/pkg/model"
	"github.com/JyotinderSingh/task-queue/pkg/secretbox"
	"github.com/JyotinderSingh/task-queue/pkg/store"
	"github.com/JyotinderSingh/task-queue/pkg/tools"
	"github.com/jackc/pgx/v4/pgxpool"
)

// SchedulerServer serves the dashboard-facing REST API.
type SchedulerServer struct {
	serverPort         string
	dbConnectionString string
	dbPool             *pgxpool.Pool
	agents             *store.AgentStore
	schedules          *store.ScheduleStore
	runs               *store.RunStore
	steps              *store.StepStore
	memory             *store.MemoryStore
	secrets            *store.SecretStore
	users              *store.UserStore
	registry           *tools.Registry
	clock              clock.Clock
	ctx                context.Context
	cancel             context.CancelFunc
	httpServer         *http.Server
}

// NewServer creates and returns a new SchedulerServer.
func NewServer(port string, dbConnectionString string) *SchedulerServer {
	return NewServerWithClock(port, dbConnectionString, clock.Real{})
}

// NewServerWithClock is used by tests, which drive time by hand because
// timezone and daylight-saving behaviour cannot be exercised in real time.
func NewServerWithClock(port string, dbConnectionString string, clk clock.Clock) *SchedulerServer {
	ctx, cancel := context.WithCancel(context.Background())
	return &SchedulerServer{
		serverPort:         port,
		dbConnectionString: dbConnectionString,
		clock:              clk,
		ctx:                ctx,
		cancel:             cancel,
	}
}

// Start initializes and starts the SchedulerServer.
func (s *SchedulerServer) Start() error {
	var err error
	s.dbPool, err = common.ConnectToDatabase(s.ctx, s.dbConnectionString)
	if err != nil {
		return err
	}

	if err := db.Migrate(s.ctx, s.dbPool); err != nil {
		return err
	}
	s.agents = store.NewAgentStore(s.dbPool)
	s.schedules = store.NewScheduleStore(s.dbPool)
	s.runs = store.NewRunStore(s.dbPool)
	s.steps = store.NewStepStore(s.dbPool)
	s.memory = store.NewMemoryStore(s.dbPool)
	s.users = store.NewUserStore(s.dbPool)

	sealer, err := secretbox.NewFromEnv()
	if err != nil {
		return err
	}
	s.secrets = store.NewSecretStore(s.dbPool, sealer)

	// An installation is closed from its first boot rather than after a setup
	// step the operator might never reach.
	if password := os.Getenv("TASKD_PASSWORD"); password != "" {
		if err := s.users.EnsurePassword(s.ctx, model.OwnerUserID, password); err != nil {
			return err
		}
	}
	hasPassword, err := s.users.HasPassword(s.ctx, model.OwnerUserID)
	if err != nil {
		return err
	}
	if !hasPassword {
		return fmt.Errorf("no password is set: start once with TASKD_PASSWORD to set one")
	}
	s.registry = tools.BuildRegistry(s.dbPool, tools.ConfigFromEnv())

	// A per-server mux rather than the default one, so that more than one
	// server can exist in a process - which the integration tests rely on.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	// The dashboard is served by this process, so one command brings up a
	// working URL rather than a URL and a second container to point at it.
	mux.Handle("/", dashboard.Handler())
	s.registerAgentRoutes(mux)
	s.registerScheduleRoutes(mux)
	s.registerAuthRoutes(mux)
	s.registerSecretRoutes(mux)

	s.httpServer = &http.Server{
		Addr:    s.serverPort,
		Handler: s.requireSession(mux),
	}

	log.Printf("Starting scheduler server on %s\n", s.serverPort)

	// Start the server in a separate goroutine
	go func() {
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %s\n", err)
		}
	}()

	// Return awaitShutdown
	return s.awaitShutdown()
}

func (s *SchedulerServer) awaitShutdown() error {
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	return s.Stop()
}

// Stop gracefully shuts down the SchedulerServer and the database connection pool.
func (s *SchedulerServer) Stop() error {
	s.dbPool.Close()
	if s.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return s.httpServer.Shutdown(ctx)
	}
	log.Println("Scheduler server and database pool stopped")
	return nil
}

// handleHealth reports whether the server can reach its database.
func (s *SchedulerServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	if err := s.dbPool.Ping(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
