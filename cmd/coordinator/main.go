package main

import (
	"flag"
	"log"

	"github.com/trungnguyen21/Taskd/pkg/common"
	"github.com/trungnguyen21/Taskd/pkg/coordinator"
)

var (
	coordinatorPort = flag.String("coordinator_port", ":8080", "Port on which the Coordinator serves requests.")
	healthPort      = flag.String("health_port", ":8090", "Port on which liveness and readiness are served.")
)

func main() {
	flag.Parse()

	server := coordinator.NewServer(*coordinatorPort, common.GetDBConnectionString())
	server.SetHealthAddress(*healthPort)

	if err := server.Start(); err != nil {
		log.Fatalf("Coordinator stopped: %v", err)
	}
}
