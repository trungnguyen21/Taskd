# Database credentials
export POSTGRES_DB="taskd"
export POSTGRES_USER="postgres"
export POSTGRES_PASSWORD="postgres"
export POSTGRES_HOST="localhost" # Or wherever your DB is running
export POSTGRES_PORT="5432"

# Security & Dashboard
# Generate a secret key (e.g. using: openssl rand -base64 32)
export TASKD_SECRET_KEY="sewbYgd6MJNk4UyFGC7koN9DZ5I/fP+JMXePxDzxFB4="
export TASKD_PASSWORD="admin"

go run cmd/scheduler/main.go
