package tools

import (
	"os"

	"github.com/JyotinderSingh/task-queue/pkg/store"
	"github.com/jackc/pgx/v4/pgxpool"
)

// Config is what an installation needs in order to offer its tools. Credentials
// come from the operator's environment until stored credentials land.
type Config struct {
	// AllowPrivateAddresses lets agents reach the local network. Off by
	// default: a tool that takes a URL from the model is otherwise an SSRF
	// engine pointed at the deployment's own network.
	AllowPrivateAddresses bool

	SearchAPIKey  string
	SearchBaseURL string

	// TelegramBaseURL points the Telegram client somewhere else, for tests.
	// The token and chat id are per-user and read from the database.
	TelegramBaseURL string
}

// ConfigFromEnv reads the tool configuration an operator has set.
func ConfigFromEnv() Config {
	return Config{
		AllowPrivateAddresses: os.Getenv("TASKD_ALLOW_PRIVATE_FETCH") == "true",
		SearchAPIKey:          os.Getenv("TASKD_SEARCH_API_KEY"),
		SearchBaseURL:         os.Getenv("TASKD_SEARCH_BASE_URL"),
		TelegramBaseURL:       os.Getenv("TASKD_TELEGRAM_BASE_URL"),
	}
}

// BuildRegistry assembles the catalogue an installation offers.
//
// A tool whose credential is missing is not registered at all. A catalogue that
// lists a tool which fails the moment an agent calls it is worse than one that
// is honest about what this installation can do.
func BuildRegistry(pool *pgxpool.Pool, settings TelegramSettings, config Config) *Registry {
	registry := NewRegistry()

	registry.Register(NewHTTPFetch(config.AllowPrivateAddresses))
	registry.Register(NewSendWebhook(config.AllowPrivateAddresses))

	if pool != nil {
		memory := store.NewMemoryStore(pool)
		registry.Register(NewMemoryRead(memory))
		registry.Register(NewMemoryWrite(memory))
	}

	if config.SearchAPIKey != "" {
		registry.Register(NewWebSearch(config.SearchBaseURL, config.SearchAPIKey))
	}

	// Telegram is always registered. Whether it is offered to a given user
	// depends on their settings, which they change while the process runs.
	if settings != nil {
		registry.Register(NewSendTelegram(config.TelegramBaseURL, settings))
	}

	return registry
}
