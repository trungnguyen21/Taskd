package tests

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/JyotinderSingh/task-queue/pkg/coordinator"
	pb "github.com/JyotinderSingh/task-queue/pkg/grpcapi"
	"github.com/JyotinderSingh/task-queue/pkg/scheduler"
	"github.com/JyotinderSingh/task-queue/pkg/worker"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
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
	database           testcontainers.Container
	databasePort       string
}

func (c *Cluster) LaunchCluster(schedulerPort string, coordinatorPort string, numWorkers int8) {
	// Launch database
	if err := c.createDatabase(); err != nil {
		log.Fatalf("Could not launch database container: %+v", err)
	}

	c.coordinatorAddress = "localhost" + coordinatorPort
	c.coordinator = coordinator.NewServer(coordinatorPort, c.dbConnectionString())
	startServer(c.coordinator)

	c.scheduler = scheduler.NewServer(schedulerPort, c.dbConnectionString())
	startServer(c.scheduler)

	c.workers = make([]*worker.WorkerServer, numWorkers)
	for i := 0; i < int(numWorkers); i++ {
		c.workers[i] = worker.NewServer("", c.coordinatorAddress)
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

	if c.database != nil {
		c.database.Terminate(context.Background())
	}
}

// LaunchAPI starts a database and the API service alone. Tests that only drive
// the dashboard-facing API do not need a coordinator or workers, and skipping
// them keeps those tests fast.
func (c *Cluster) LaunchAPI(schedulerPort string) {
	if err := c.createDatabase(); err != nil {
		log.Fatalf("Could not launch database container: %+v", err)
	}

	c.StartAPI(schedulerPort)
}

// StartAPI starts an API service against the cluster's existing database. It is
// separate from LaunchAPI so that a test can restart the service - which is how
// migrations are exercised against a database that already has a schema.
func (c *Cluster) StartAPI(schedulerPort string) {
	c.scheduler = scheduler.NewServer(schedulerPort, c.dbConnectionString())
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

func (c *Cluster) createDatabase() error {
	ctx := context.Background()

	// Define the container request using your custom image
	req := testcontainers.ContainerRequest{
		Image:        "scheduler-postgres", // Use your custom image
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_PASSWORD": postgresPassword,
			"POSTGRES_USER":     postgresUser,
			"POSTGRES_DB":       postgresDb,
		},
		WaitingFor: wait.ForListeningPort("5432/tcp"),
	}

	// Start the container
	var err error
	c.database, err = testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return err
	}

	// The container gets a random host port, so that tests do not collide with
	// anything already listening on 5432.
	port, err := c.database.MappedPort(ctx, "5432")
	if err != nil {
		return err
	}
	c.databasePort = port.Port()
	return nil
}

func CreateTestClient(coordinatorAddress string) (*grpc.ClientConn, pb.CoordinatorServiceClient) {
	conn, err := grpc.Dial(coordinatorAddress, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	if err != nil {
		log.Fatal("Could not create test connection to coordinator")
	}
	return conn, pb.NewCoordinatorServiceClient(conn)
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
