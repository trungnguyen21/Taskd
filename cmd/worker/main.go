package main

import (
	"context"
	"flag"
	"log"
	"os"

	"github.com/JyotinderSingh/task-queue/pkg/clock"
	"github.com/JyotinderSingh/task-queue/pkg/common"
	"github.com/JyotinderSingh/task-queue/pkg/executor"
	"github.com/JyotinderSingh/task-queue/pkg/tools"
	"github.com/JyotinderSingh/task-queue/pkg/worker"
)

var (
	serverPort      = flag.String("worker_port", "", "Port on which the Worker serves requests.")
	coordinatorPort = flag.String("coordinator", ":8080", "Network address of the Coordinator.")
)

func main() {
	flag.Parse()

	ctx := context.Background()

	// Workers read the database directly: Postgres is the system of record, and
	// the coordinator is a participant in it rather than a gatekeeper.
	pool, err := common.ConnectToDatabase(ctx, common.GetDBConnectionString())
	if err != nil {
		log.Fatalf("Could not connect to the database: %v", err)
	}
	defer pool.Close()

	registry := tools.BuildRegistry(pool, tools.ConfigFromEnv())

	// The provider credential comes from the environment until stored
	// credentials land.
	agentExecutor := executor.New(pool, registry, clock.Real{}, os.Getenv("TASKD_MODEL_API_KEY"))

	server := worker.NewServerWithExecutor(*serverPort, *coordinatorPort, agentExecutor)
	if err := server.Start(); err != nil {
		log.Fatalf("Worker stopped: %v", err)
	}
}
