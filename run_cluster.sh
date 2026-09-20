#!/bin/bash

# Database credentials
export POSTGRES_DB="taskd"
export POSTGRES_USER="postgres"
export POSTGRES_PASSWORD="postgres"
export POSTGRES_HOST="localhost" 
export POSTGRES_PORT="5432"

# Security & Dashboard
export TASKD_SECRET_KEY="sewbYgd6MJNk4UyFGC7koN9DZ5I/fP+JMXePxDzxFB4="
export TASKD_PASSWORD="admin"

# You NEED a model API key to run agents! (OpenAI format)
# Replace this with a real key if you have one.
export TASKD_MODEL_API_KEY="sk-your-openai-api-key"

echo "Starting Coordinator..."
go run cmd/coordinator/main.go &
COORD_PID=$!

echo "Starting Worker..."
go run cmd/worker/main.go --worker_port=":50051" &
WORKER_PID=$!

echo "Starting Scheduler & Dashboard..."
go run cmd/scheduler/main.go &
SCHED_PID=$!

# Trap Ctrl+C to kill all background processes
trap "kill $COORD_PID $WORKER_PID $SCHED_PID" SIGINT

# Keep script running
wait
