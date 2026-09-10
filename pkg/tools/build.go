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

	TelegramToken   string
	TelegramChatID  string
	TelegramBaseURL string
}

// ConfigFromEnv reads the tool configuration an operator has set.
func ConfigFromEnv() Config {
	return Config{
		AllowPrivateAddresses: os.Getenv("TASKD_ALLOW_PRIVATE_FETCH") == "true",
		SearchAPIKey:          os.Getenv("TASKD_SEARCH_API_KEY"),
		SearchBaseURL:         os.Getenv("TASKD_SEARCH_BASE_URL"),
		TelegramToken:         os.Getenv("TASKD_TELEGRAM_BOT_TOKEN"),
		TelegramChatID:        os.Getenv("TASKD_TELEGRAM_CHAT_ID"),
		TelegramBaseURL:       os.Getenv("TASKD_TELEGRAM_BASE_URL"),
	}
}

// BuildRegistry assembles the catalogue an installation offers.
//
// A tool whose credential is missing is not registered at all. A catalogue that
// lists a tool which fails the moment an agent calls it is worse than one that
// is honest about what this installation can do.
func BuildRegistry(pool *pgxpool.Pool, config Config) *Registry {
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

	if config.TelegramToken != "" && config.TelegramChatID != "" {
		registry.Register(NewSendTelegram(config.TelegramBaseURL,
			config.TelegramToken, config.TelegramChatID))
	}

	return registry
}
