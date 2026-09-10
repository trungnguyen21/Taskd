package tests

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/JyotinderSingh/task-queue/pkg/notifier"
	"github.com/JyotinderSingh/task-queue/pkg/store"
	"github.com/JyotinderSingh/task-queue/pkg/tools"
)

type inbox struct {
	Items []struct {
		RunID     string  `json:"run_id"`
		AgentName string  `json:"agent_name"`
		Status    string  `json:"status"`
		Output    string  `json:"output"`
		ReadAt    *string `json:"read_at"`
	} `json:"items"`
	UnreadCount int `json:"unread_count"`
}

func readInbox(t *testing.T) inbox {
	t.Helper()
	status, body := apiRequest(t, http.MethodGet, "/api/inbox", nil)
	if status != http.StatusOK {
		t.Fatalf("Expected 200 reading the inbox, got %d: %s", status, body)
	}
	var result inbox
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("Failed to decode the inbox: %v (body: %s)", err, body)
	}
	return result
}

// Every run's output lands in the inbox whether or not the agent chose to
// notify, so Taskd is useful before a bot is configured and a confused agent
// that forgets to send still leaves something to read.
func TestRunOutputLandsInTheInbox(t *testing.T) {
	model := launchWithModel(t)
	defer teardown()

	model.queue(textResponse("Markets closed higher."))

	agentID := agentUsing(t, model, "Digest", nil, nil)
	runID := triggerRun(t, agentID)
	waitForRunStatus(t, runID, "succeeded")

	box := readInbox(t)
	if len(box.Items) != 1 {
		t.Fatalf("Expected one item in the inbox, got %d", len(box.Items))
	}
	if box.Items[0].Output != "Markets closed higher." {
		t.Errorf("Expected the run's output, got %q", box.Items[0].Output)
	}
	if box.Items[0].AgentName != "Digest" {
		t.Errorf("Expected the agent's name alongside its output, got %q", box.Items[0].AgentName)
	}
	if box.UnreadCount != 1 {
		t.Errorf("Expected one unread item, got %d", box.UnreadCount)
	}

	status, _ := apiRequest(t, http.MethodPost, "/api/runs/"+runID+"/read", nil)
	if status != http.StatusNoContent {
		t.Fatalf("Expected 204 marking a run read, got %d", status)
	}

	box = readInbox(t)
	if box.UnreadCount != 0 {
		t.Errorf("Expected nothing unread after reading, got %d", box.UnreadCount)
	}
	if box.Items[0].ReadAt == nil {
		t.Error("Expected the item to record when it was read")
	}

	// Marking a run that does not exist is still a missing run.
	status, _ = apiRequest(t, http.MethodPost, "/api/runs/not-a-run/read", nil)
	if status != http.StatusNotFound {
		t.Errorf("Expected 404 marking an unknown run read, got %d", status)
	}
}

// A failed run is in the inbox too: silence is what the product exists to
// prevent.
func TestFailedRunIsVisibleInTheInbox(t *testing.T) {
	model := launchWithModel(t)
	defer teardown()

	model.failWithStatus(http.StatusUnauthorized)

	agentID := agentUsing(t, model, "Broken", nil, nil)
	runID := triggerRun(t, agentID)
	waitForRunStatus(t, runID, "failed")

	box := readInbox(t)
	if len(box.Items) != 1 || box.Items[0].Status != "failed" {
		t.Fatalf("Expected the failed run in the inbox, got %+v", box.Items)
	}
	if box.UnreadCount != 1 {
		t.Errorf("Expected the failure to be unread, got %d", box.UnreadCount)
	}
	_ = runID
}

// Telegram settings are per-user and stored, not baked into the process.
func TestTelegramSettingsAreStoredAndReported(t *testing.T) {
	cluster = Cluster{}
	cluster.LaunchAPI(apiPort)
	defer teardown()

	status, body := apiRequest(t, http.MethodGet, "/api/settings", nil)
	if status != http.StatusOK {
		t.Fatalf("Expected 200 reading settings, got %d: %s", status, body)
	}
	var settings store.Settings
	json.Unmarshal(body, &settings)
	if settings.TelegramConfigured {
		t.Error("Expected Telegram to start unconfigured")
	}
	if !settings.FailureAlertsEnabled {
		t.Error("Expected failure alerts to be on by default")
	}

	// A chat id alone is not enough: both halves are needed to reach anyone.
	apiRequest(t, http.MethodPut, "/api/settings",
		map[string]interface{}{"telegram_chat_id": "4242"})
	_, body = apiRequest(t, http.MethodGet, "/api/settings", nil)
	json.Unmarshal(body, &settings)
	if settings.TelegramConfigured {
		t.Error("Expected a chat id without a token not to count as configured")
	}

	configureTelegram(t, "bot-token", "4242")
	_, body = apiRequest(t, http.MethodGet, "/api/settings", nil)
	json.Unmarshal(body, &settings)
	if !settings.TelegramConfigured {
		t.Error("Expected Telegram to be configured once both halves are stored")
	}

	// The token is a credential, so it is never returned.
	if strings.Contains(string(body), "bot-token") {
		t.Error("Expected the bot token not to be returned in settings")
	}
}

// The dashboard finds the chat id, rather than asking the operator to call
// getUpdates by hand and read a number out of the JSON.
func TestTelegramChatDiscovery(t *testing.T) {
	telegram, telegramServer := newRecorder(
		`{"ok":true,"result":[{"message":{"chat":{"id":987654}}}]}`)
	defer telegramServer.Close()

	cluster = Cluster{ToolConfig: tools.Config{TelegramBaseURL: telegramServer.URL}}
	cluster.LaunchAPI(apiPort)
	defer teardown()

	// Without a token there is nothing to ask.
	status, _ := apiRequest(t, http.MethodPost, "/api/telegram/detect-chat", nil)
	if status != http.StatusBadRequest {
		t.Fatalf("Expected 400 detecting a chat with no token stored, got %d", status)
	}

	apiRequest(t, http.MethodPut, "/api/secrets/"+store.TelegramTokenSecret,
		map[string]string{"value": "bot-token"})

	status, body := apiRequest(t, http.MethodPost, "/api/telegram/detect-chat", nil)
	if status != http.StatusOK {
		t.Fatalf("Expected 200 detecting the chat, got %d: %s", status, body)
	}

	var settings store.Settings
	json.Unmarshal(body, &settings)
	if settings.TelegramChatID != "987654" {
		t.Errorf("Expected the discovered chat id to be stored, got %q", settings.TelegramChatID)
	}
	if !settings.TelegramConfigured {
		t.Error("Expected Telegram to be configured after discovery")
	}
	if paths := telegram.requestedPaths(); len(paths) != 1 || !strings.Contains(paths[0], "getUpdates") {
		t.Errorf("Expected getUpdates to be called, got %v", paths)
	}
}

// Telegram is only offered to a user who can actually be reached, and that
// changes while the process is running.
func TestTelegramIsOfferedOnlyOnceConfigured(t *testing.T) {
	telegram, telegramServer := newRecorder(`{"ok":true}`)
	defer telegramServer.Close()
	_ = telegram

	cluster = Cluster{ToolConfig: tools.Config{TelegramBaseURL: telegramServer.URL}}
	cluster.LaunchAPI(apiPort)
	defer teardown()

	if toolIsOffered(t, "send_telegram") {
		t.Error("Expected send_telegram not to be offered before it is configured")
	}

	configureTelegram(t, "bot-token", "4242")

	if !toolIsOffered(t, "send_telegram") {
		t.Error("Expected send_telegram to be offered once configured")
	}
}

func toolIsOffered(t *testing.T, name string) bool {
	t.Helper()
	status, body := apiRequest(t, http.MethodGet, "/api/tools", nil)
	if status != http.StatusOK {
		t.Fatalf("Expected 200 listing tools, got %d: %s", status, body)
	}
	var offered []struct {
		Name string `json:"name"`
	}
	json.Unmarshal(body, &offered)
	for _, tool := range offered {
		if tool.Name == name {
			return true
		}
	}
	return false
}

// A failing agent cannot report its own failure, so the platform does it. One
// bad night is not worth a message; three in a row is a broken agent.
func TestRepeatedFailuresAreReportedByThePlatform(t *testing.T) {
	telegram, telegramServer := newRecorder(`{"ok":true}`)
	defer telegramServer.Close()

	model := newFakeModel()
	defer model.close()
	model.always(textResponse("done"))

	cluster = Cluster{Model: model, ToolConfig: tools.Config{TelegramBaseURL: telegramServer.URL}}
	cluster.LaunchCluster(apiPort, coordinatorPort, 1)
	defer teardown()

	configureTelegram(t, "bot-token", "4242")

	model.failWithStatus(http.StatusUnauthorized)
	agentID := agentUsing(t, model, "Morning brief", nil, nil)

	// Two failures are not yet worth reporting.
	for i := 0; i < notifier.FailureThreshold-1; i++ {
		waitForRunStatus(t, triggerRun(t, agentID), "failed")
	}
	reported, err := cluster.Notifier.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("Reporting failed: %v", err)
	}
	if reported != 0 {
		t.Fatalf("Expected no report below the threshold, got %d", reported)
	}

	// The third crosses it.
	waitForRunStatus(t, triggerRun(t, agentID), "failed")
	reported, err = cluster.Notifier.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("Reporting failed: %v", err)
	}
	if reported != 1 {
		t.Fatalf("Expected the failing agent to be reported, got %d", reported)
	}

	sent := telegram.received()
	if len(sent) != 1 {
		t.Fatalf("Expected one alert, got %d", len(sent))
	}
	if !strings.Contains(sent[0], "Morning brief") {
		t.Errorf("Expected the alert to name the agent, got %s", sent[0])
	}
	if !strings.Contains(sent[0], "401") {
		t.Errorf("Expected the alert to carry the reason, got %s", sent[0])
	}

	// It is reported once, not on every tick.
	reported, _ = cluster.Notifier.RunOnce(context.Background())
	if reported != 0 {
		t.Fatalf("Expected the agent not to be reported twice, got %d", reported)
	}

	// Recovering clears the count, so a later break is reported again.
	model.failWithStatus(0)
	waitForRunStatus(t, triggerRun(t, agentID), "succeeded")

	model.failWithStatus(http.StatusUnauthorized)
	for i := 0; i < notifier.FailureThreshold; i++ {
		waitForRunStatus(t, triggerRun(t, agentID), "failed")
	}
	reported, _ = cluster.Notifier.RunOnce(context.Background())
	if reported != 1 {
		t.Fatalf("Expected a recovered agent that breaks again to be reported, got %d", reported)
	}
}

// An operator who does not want alerts does not get them.
func TestFailureAlertsCanBeTurnedOff(t *testing.T) {
	telegram, telegramServer := newRecorder(`{"ok":true}`)
	defer telegramServer.Close()

	model := newFakeModel()
	defer model.close()
	model.always(textResponse("done"))

	cluster = Cluster{Model: model, ToolConfig: tools.Config{TelegramBaseURL: telegramServer.URL}}
	cluster.LaunchCluster(apiPort, coordinatorPort, 1)
	defer teardown()

	configureTelegram(t, "bot-token", "4242")
	apiRequest(t, http.MethodPut, "/api/settings",
		map[string]interface{}{"telegram_chat_id": "4242", "failure_alerts_enabled": false})

	model.failWithStatus(http.StatusUnauthorized)
	agentID := agentUsing(t, model, "Quiet", nil, nil)
	for i := 0; i < notifier.FailureThreshold; i++ {
		waitForRunStatus(t, triggerRun(t, agentID), "failed")
	}

	reported, err := cluster.Notifier.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("Reporting failed: %v", err)
	}
	if reported != 0 {
		t.Fatalf("Expected no alerts when they are turned off, got %d", reported)
	}
	if sent := telegram.received(); len(sent) != 0 {
		t.Fatalf("Expected nothing to be sent, got %d messages", len(sent))
	}
}

// configureTelegram stores the delivery settings the way the dashboard does:
// the bot token as a sealed credential, the chat id as a plain setting.
func configureTelegram(t *testing.T, token, chatID string) {
	t.Helper()

	status, body := apiRequest(t, http.MethodPut,
		"/api/secrets/"+store.TelegramTokenSecret, map[string]string{"value": token})
	if status != http.StatusOK {
		t.Fatalf("Expected 200 storing the bot token, got %d: %s", status, body)
	}

	status, body = apiRequest(t, http.MethodPut, "/api/settings",
		map[string]interface{}{"telegram_chat_id": chatID})
	if status != http.StatusOK {
		t.Fatalf("Expected 200 storing the chat id, got %d: %s", status, body)
	}
}
