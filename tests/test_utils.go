package tests

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v4/pgxpool"

	"github.com/JyotinderSingh/task-queue/pkg/clock"
	"github.com/JyotinderSingh/task-queue/pkg/coordinator"
	"github.com/JyotinderSingh/task-queue/pkg/executor"
	"github.com/JyotinderSingh/task-queue/pkg/materializer"
	"github.com/JyotinderSingh/task-queue/pkg/model"
	"github.com/JyotinderSingh/task-queue/pkg/notifier"
	"github.com/JyotinderSingh/task-queue/pkg/reaper"
	"github.com/JyotinderSingh/task-queue/pkg/scheduler"
	"github.com/JyotinderSingh/task-queue/pkg/secretbox"
	"github.com/JyotinderSingh/task-queue/pkg/store"
	"github.com/JyotinderSingh/task-queue/pkg/tools"
	"github.com/JyotinderSingh/task-queue/pkg/worker"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	// testPassword and testMasterKey stand in for what an operator sets.
	testPassword  = "test-password"
	testMasterKey = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="

	postgresUser     = "postgres"
	postgresPassword = "postgres"
	postgresDb       = "scheduler"
	postgresHost     = "localhost"
)

type Cluster struct {
	coordinatorAddress string
	scheduler          *scheduler.SchedulerServer
	coordinator        *coordinator.CoordinatorServer
	workers            []*worker.WorkerServer
	databasePort       string
	Clock              *clock.Fake
	Materializer       *materializer.Materializer
	Reaper             *reaper.Reaper
	Notifier           *notifier.Notifier
	// Model is a default fake model endpoint, so that any agent created by a
	// test has somewhere to talk to. Tests that script specific responses build
	// their own and point an agent at it instead.
	Model *fakeModel
	// ToolConfig is applied when the cluster's workers are built, so a test can
	// stand up fake Telegram and search endpoints.
	ToolConfig tools.Config
	// CoordinatorHealthPort enables the coordinator's probes. Empty leaves them
	// off, so several clusters can exist in one test process.
	CoordinatorHealthPort string
	// DB is exposed so that a test can set up states the API cannot reach,
	// such as a run abandoned by a worker that died.
	DB *pgxpool.Pool
	// Session is the cookie every API request carries, since the API is closed
	// to anyone without one.
	Session *http.Cookie
	// Settings is the per-user configuration the tools and notifier resolve
	// through.
	Settings *store.SettingsStore
}

func (c *Cluster) LaunchCluster(schedulerPort string, coordinatorPort string, numWorkers int8) {
	// Launch database
	if err := c.createDatabase(); err != nil {
		log.Fatalf("Could not launch database container: %+v", err)
	}

	c.startDefaultModel()

	if c.Clock == nil {
		c.Clock = clock.NewFake(time.Now())
	}

	c.coordinatorAddress = "localhost" + coordinatorPort
	c.coordinator = coordinator.NewServerWithClock(coordinatorPort, c.dbConnectionString(), c.Clock)
	// Tests should not spend a production scan period waiting for each run.
	c.coordinator.SetScanInterval(250 * time.Millisecond)
	if c.CoordinatorHealthPort != "" {
		c.coordinator.SetHealthAddress(c.CoordinatorHealthPort)
	}
	startServer(c.coordinator)

	c.StartAPI(schedulerPort)

	// Workers run the real executor and the real tool registry. What is faked
	// are the endpoints those tools talk to, which are configuration rather
	// than code.
	config := c.ToolConfig
	config.AllowPrivateAddresses = true

	sealer, err := secretbox.New(testMasterKey)
	if err != nil {
		log.Fatalf("Could not build the sealer: %v", err)
	}
	secrets := store.NewSecretStore(c.DB, sealer)
	c.Settings = store.NewSettingsStore(c.DB, secrets)

	registry := tools.BuildRegistry(c.DB, c.Settings, config)

	c.workers = make([]*worker.WorkerServer, numWorkers)
	for i := 0; i < int(numWorkers); i++ {
		agentExecutor := executor.New(c.DB, secrets, registry, c.Clock, "test-key")
		c.workers[i] = worker.NewServerWithExecutor("", c.coordinatorAddress, agentExecutor)
		startServer(c.workers[i])
	}

	c.waitForWorkers()
}

func (c *Cluster) StopCluster() {
	for _, worker := range c.workers {
		if err := worker.Stop(); err != nil {
			log.Printf("Failed to stop worker: %v", err)
		}
	}
	if c.coordinator != nil {
		if err := c.coordinator.Stop(); err != nil {
			log.Printf("Failed to stop coordinator: %v", err)
		}
	}

	if c.scheduler != nil {
		if err := c.scheduler.Stop(); err != nil {
			log.Printf("Failed to stop scheduler: %v", err)
		}
	}

	if c.Model != nil {
		c.Model.close()
		c.Model = nil
	}

	if c.DB != nil {
		c.DB.Close()
		c.DB = nil
	}
}

// LaunchAPI starts a database and the API service alone. Tests that only drive
// the dashboard-facing API do not need a coordinator or workers, and skipping
// them keeps those tests fast.
func (c *Cluster) LaunchAPI(schedulerPort string) {
	if err := c.createDatabase(); err != nil {
		log.Fatalf("Could not launch database container: %+v", err)
	}

	c.startDefaultModel()

	c.StartAPI(schedulerPort)
}

// StartAPI starts an API service against the cluster's existing database. It is
// separate from LaunchAPI so that a test can restart the service - which is how
// migrations are exercised against a database that already has a schema.
//
// The service and the materializer share a clock the test drives by hand, so
// schedule behaviour is exercised without waiting in real time.
func (c *Cluster) StartAPI(schedulerPort string) {
	if c.Clock == nil {
		c.Clock = clock.NewFake(time.Now())
	}
	// An installation refuses to start without a master key for stored
	// credentials, and without a password it would be open to the network.
	os.Setenv("TASKD_SECRET_KEY", testMasterKey)
	os.Setenv("TASKD_PASSWORD", testPassword)
	os.Setenv("TASKD_TELEGRAM_BASE_URL", c.ToolConfig.TelegramBaseURL)

	c.scheduler = scheduler.NewServerWithClock(schedulerPort, c.dbConnectionString(), c.Clock)
	startServer(c.scheduler)

	if err := WaitForCondition(func() bool {
		resp, err := http.Get("http://localhost" + schedulerPort + "/healthz")
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 60*time.Second, 250*time.Millisecond); err != nil {
		log.Fatalf("API service did not become healthy: %v", err)
	}

	c.Materializer = materializer.New(c.DB, c.Clock)
	c.Reaper = reaper.New(c.DB, c.Clock)

	sealer, err := secretbox.New(testMasterKey)
	if err != nil {
		log.Fatalf("Could not build the sealer: %v", err)
	}
	settings := store.NewSettingsStore(c.DB, store.NewSecretStore(c.DB, sealer))
	c.Notifier = notifier.New(c.DB, settings, c.Clock, c.ToolConfig.TelegramBaseURL)

	c.signIn(schedulerPort)
}

// signIn logs the test in, the way the dashboard does.
func (c *Cluster) signIn(schedulerPort string) {
	body := strings.NewReader(`{"password":"` + testPassword + `"}`)
	response, err := http.Post("http://localhost"+schedulerPort+"/api/login",
		"application/json", body)
	if err != nil {
		log.Fatalf("Could not sign in: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		log.Fatalf("Signing in returned %d", response.StatusCode)
	}
	for _, cookie := range response.Cookies() {
		if cookie.Name == "taskd_session" {
			c.Session = cookie
			return
		}
	}
	log.Fatal("Signing in returned no session cookie")
}

// StopAPI stops the API service, leaving the database running.
func (c *Cluster) StopAPI() {
	if c.scheduler != nil {
		if err := c.scheduler.Stop(); err != nil {
			log.Printf("Failed to stop scheduler: %v", err)
		}
		c.scheduler = nil
	}
}

func startServer(srv interface {
	Start() error
}) {
	go func() {
		if err := srv.Start(); err != nil {
			log.Fatalf("Failed to start server: %v", err)
		}
	}()
}

func (c *Cluster) waitForWorkers() {
	for {
		c.coordinator.WorkerPoolMutex.Lock()

		c.coordinator.WorkerPoolKeysMutex.RLock()
		if len(c.coordinator.WorkerPoolKeys) == len(c.workers) {
			c.coordinator.WorkerPoolKeysMutex.RUnlock()
			c.coordinator.WorkerPoolMutex.Unlock()
			break
		}
		c.coordinator.WorkerPoolKeysMutex.RUnlock()
		c.coordinator.WorkerPoolMutex.Unlock()
		time.Sleep(time.Second)
	}
}

func (c *Cluster) dbConnectionString() string {
	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s",
		postgresUser, postgresPassword, postgresHost, c.databasePort, postgresDb)
}

// One database container serves the whole package. Starting one per test was
// the dominant cost of the suite: tests that ran no agents at all cost as much
// as tests that ran several.
var (
	sharedDatabase     testcontainers.Container
	sharedDatabasePort string
	sharedDatabaseOnce sync.Once
	sharedDatabaseErr  error
)

// startSharedDatabase starts the container the first time it is asked for.
func startSharedDatabase() (string, error) {
	sharedDatabaseOnce.Do(func() {
		ctx := context.Background()

		request := testcontainers.ContainerRequest{
			Image:        "postgres:16.1",
			ExposedPorts: []string{"5432/tcp"},
			Env: map[string]string{
				"POSTGRES_PASSWORD": postgresPassword,
				"POSTGRES_USER":     postgresUser,
				"POSTGRES_DB":       postgresDb,
			},
			WaitingFor: wait.ForListeningPort("5432/tcp"),
		}

		sharedDatabase, sharedDatabaseErr = testcontainers.GenericContainer(ctx,
			testcontainers.GenericContainerRequest{ContainerRequest: request, Started: true})
		if sharedDatabaseErr != nil {
			return
		}

		// A random host port, so a run does not collide with anything already
		// listening on 5432.
		port, err := sharedDatabase.MappedPort(ctx, "5432")
		if err != nil {
			sharedDatabaseErr = err
			return
		}
		sharedDatabasePort = port.Port()
	})

	return sharedDatabasePort, sharedDatabaseErr
}

// stopSharedDatabase is called once the package's tests are done.
func stopSharedDatabase() {
	if sharedDatabase != nil {
		sharedDatabase.Terminate(context.Background())
	}
}

// createDatabase attaches to the shared container and clears whatever the
// previous test left behind.
//
// Sharing the container means state has to be reset explicitly, so it is done
// here rather than left to individual tests: one test's leftover rows becoming
// another's mystery failure is the failure mode this has to avoid.
func (c *Cluster) createDatabase() error {
	port, err := startSharedDatabase()
	if err != nil {
		return err
	}
	c.databasePort = port

	pool, err := pgxpool.Connect(context.Background(), c.dbConnectionString())
	if err != nil {
		return err
	}
	c.DB = pool

	return c.resetDatabase()
}

// resetDatabase empties every table a test can write to. The owner row survives,
// with its settings returned to their defaults.
func (c *Cluster) resetDatabase() error {
	ctx := context.Background()

	// Nothing to clear before the first migration has ever run.
	var migrated bool
	if err := c.DB.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM information_schema.tables
			WHERE table_name = 'agents')`).Scan(&migrated); err != nil {
		return err
	}
	if !migrated {
		return nil
	}

	if _, err := c.DB.Exec(ctx, `TRUNCATE agents, runs, run_steps, schedules,
		memory_records, secrets, sessions RESTART IDENTITY CASCADE`); err != nil {
		return err
	}

	_, err := c.DB.Exec(ctx, `UPDATE users
		SET telegram_chat_id = '', failure_alerts_enabled = TRUE`)
	return err
}

func WaitForCondition(condition func() bool, timeout time.Duration, retryInterval time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	ticker := time.NewTicker(retryInterval)
	defer ticker.Stop()

	for {
		select {
		case <-timer.C:
			return fmt.Errorf("timeout exceeded")
		case <-ticker.C:
			if condition() {
				return nil
			}
		}
	}
}

// clockAt builds a fake clock for a cluster that is about to be launched.
func clockAt(t time.Time) *clock.Fake {
	return clock.NewFake(t)
}

// startDefaultModel gives the cluster a model endpoint that always answers, so
// that agents created by tests which do not care about the model still run.
func (c *Cluster) startDefaultModel() {
	if c.Model != nil {
		return
	}
	c.Model = newFakeModel()
	c.Model.always(textResponse("done"))
}

// ownerUserID is the single user of a test install.
const ownerUserID = model.OwnerUserID
