// Package notifier tells the operator when an agent has stopped working.
//
// A failing agent cannot report its own failure: if its model credential is
// wrong, the agent is exactly the thing that cannot run. These agents run
// unattended and their whole output is a message, so a broken one produces
// nothing - which is indistinguishable from "I did not check my messages". The
// platform has to say something.
package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/JyotinderSingh/task-queue/pkg/clock"
	"github.com/JyotinderSingh/task-queue/pkg/model"
	"github.com/JyotinderSingh/task-queue/pkg/store"
	"github.com/jackc/pgx/v4/pgxpool"
)

// FailureThreshold is how many consecutive failures make an agent worth
// reporting. One failure is a bad night; three in a row is a broken agent.
const FailureThreshold = 3

const sendTimeout = 30 * time.Second

// Notifier reports failing agents to the operator.
type Notifier struct {
	pool     *pgxpool.Pool
	settings *store.SettingsStore
	clock    clock.Clock
	baseURL  string
	client   *http.Client
}

func New(pool *pgxpool.Pool, settings *store.SettingsStore, clk clock.Clock, telegramBaseURL string) *Notifier {
	if telegramBaseURL == "" {
		telegramBaseURL = "https://api.telegram.org"
	}
	return &Notifier{
		pool:     pool,
		settings: settings,
		clock:    clk,
		baseURL:  strings.TrimSuffix(telegramBaseURL, "/"),
		client:   &http.Client{Timeout: sendTimeout},
	}
}

type failingAgent struct {
	id       string
	userID   string
	name     string
	failures int
	lastErr  string
}

// RunOnce reports agents that have crossed the failure threshold since they
// were last reported, and returns how many were reported.
func (n *Notifier) RunOnce(ctx context.Context) (int, error) {
	now := n.clock.Now()

	rows, err := n.pool.Query(ctx, `SELECT a.id, a.user_id, a.name, a.consecutive_failures,
			COALESCE((SELECT r.error FROM runs r
				WHERE r.agent_id = a.id AND r.status = $1
				ORDER BY r.finished_at DESC NULLS LAST LIMIT 1), '')
		FROM agents a
		JOIN users u ON u.id = a.user_id
		WHERE u.failure_alerts_enabled
		  AND a.consecutive_failures >= $2
		  AND a.failure_notified_at IS NULL`,
		model.RunFailed, FailureThreshold)
	if err != nil {
		return 0, err
	}

	failing := []failingAgent{}
	for rows.Next() {
		var agent failingAgent
		if err := rows.Scan(&agent.id, &agent.userID, &agent.name,
			&agent.failures, &agent.lastErr); err != nil {
			rows.Close()
			return 0, err
		}
		failing = append(failing, agent)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	reported := 0
	for _, agent := range failing {
		message := fmt.Sprintf("Taskd: %q has failed %d times in a row and is not producing results.",
			agent.name, agent.failures)
		if agent.lastErr != "" {
			message += "\n\nLast error: " + agent.lastErr
		}

		if err := n.send(ctx, agent.userID, message); err != nil {
			// A delivery that fails is left unmarked, so it is retried on the
			// next tick rather than lost.
			log.Printf("Could not report failing agent %s: %v", agent.id, err)
			continue
		}

		if _, err := n.pool.Exec(ctx,
			`UPDATE agents SET failure_notified_at = $2 WHERE id = $1`, agent.id, now); err != nil {
			return reported, err
		}
		reported++
	}

	return reported, nil
}

// send delivers the alert through the operator's own notification channel.
func (n *Notifier) send(ctx context.Context, userID, message string) error {
	token, chatID, ok := n.settings.TelegramDelivery(ctx, userID)
	if !ok {
		return fmt.Errorf("no notification channel is configured")
	}

	body, err := json.Marshal(map[string]string{"chat_id": chatID, "text": message})
	if err != nil {
		return err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/bot%s/sendMessage", n.baseURL, token), bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := n.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram returned %d", response.StatusCode)
	}
	return nil
}
