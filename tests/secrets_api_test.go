package tests

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/JyotinderSingh/task-queue/pkg/store"
)

func listSecrets(t *testing.T) []store.Secret {
	t.Helper()
	status, body := apiRequest(t, http.MethodGet, "/api/secrets", nil)
	if status != http.StatusOK {
		t.Fatalf("Expected 200 listing secrets, got %d: %s", status, body)
	}
	var secrets []store.Secret
	if err := json.Unmarshal(body, &secrets); err != nil {
		t.Fatalf("Failed to decode secrets: %v (body: %s)", err, body)
	}
	return secrets
}

// A credential goes in and never comes back out.
func TestStoredCredentialIsNeverReturned(t *testing.T) {
	cluster = Cluster{}
	cluster.LaunchAPI(apiPort)
	defer teardown()

	const value = "sk-ant-secret-value-4f2a"

	status, body := apiRequest(t, http.MethodPut, "/api/secrets/default",
		map[string]string{"value": value})
	if status != http.StatusOK {
		t.Fatalf("Expected 200 storing a credential, got %d: %s", status, body)
	}
	if strings.Contains(string(body), value) {
		t.Fatal("Expected the credential not to be echoed back")
	}

	var stored store.Secret
	json.Unmarshal(body, &stored)
	if stored.MaskedSuffix != "…4f2a" {
		t.Errorf("Expected a masked suffix, got %q", stored.MaskedSuffix)
	}

	secrets := listSecrets(t)
	if len(secrets) != 1 {
		t.Fatalf("Expected one stored credential, got %d", len(secrets))
	}
	if secrets[0].Name != "default" || secrets[0].MaskedSuffix != "…4f2a" {
		t.Errorf("Expected the masked credential in the list, got %+v", secrets[0])
	}

	// No response anywhere carries the value.
	_, listBody := apiRequest(t, http.MethodGet, "/api/secrets", nil)
	if strings.Contains(string(listBody), value) {
		t.Fatal("Expected the list not to contain the credential")
	}
}

// What is on disk is sealed, not merely hidden by the API.
func TestStoredCredentialIsEncryptedAtRest(t *testing.T) {
	cluster = Cluster{}
	cluster.LaunchAPI(apiPort)
	defer teardown()

	const value = "sk-ant-secret-value-4f2a"
	apiRequest(t, http.MethodPut, "/api/secrets/default", map[string]string{"value": value})

	var encrypted []byte
	err := cluster.DB.QueryRow(t.Context(),
		`SELECT encrypted_value FROM secrets WHERE name = 'default'`).Scan(&encrypted)
	if err != nil {
		t.Fatalf("Failed to read the stored row: %v", err)
	}
	if strings.Contains(string(encrypted), value) {
		t.Fatal("Expected the stored value to be sealed, not stored in the clear")
	}
	if strings.Contains(string(encrypted), "sk-ant") {
		t.Fatal("Expected no recognisable fragment of the credential on disk")
	}
}

func TestCredentialCanBeReplacedAndDeleted(t *testing.T) {
	cluster = Cluster{}
	cluster.LaunchAPI(apiPort)
	defer teardown()

	apiRequest(t, http.MethodPut, "/api/secrets/default", map[string]string{"value": "first-key-aaaa"})
	apiRequest(t, http.MethodPut, "/api/secrets/default", map[string]string{"value": "second-key-bbbb"})

	secrets := listSecrets(t)
	if len(secrets) != 1 {
		t.Fatalf("Expected replacing to keep one credential, got %d", len(secrets))
	}
	if secrets[0].MaskedSuffix != "…bbbb" {
		t.Errorf("Expected the replacement to be stored, got %q", secrets[0].MaskedSuffix)
	}

	status, _ := apiRequest(t, http.MethodDelete, "/api/secrets/default", nil)
	if status != http.StatusNoContent {
		t.Fatalf("Expected 204 deleting a credential, got %d", status)
	}
	if remaining := listSecrets(t); len(remaining) != 0 {
		t.Fatalf("Expected the credential to be gone, got %d", len(remaining))
	}

	status, _ = apiRequest(t, http.MethodDelete, "/api/secrets/default", nil)
	if status != http.StatusNotFound {
		t.Fatalf("Expected 404 deleting a credential twice, got %d", status)
	}

	status, _ = apiRequest(t, http.MethodPut, "/api/secrets/default", map[string]string{"value": "  "})
	if status != http.StatusBadRequest {
		t.Fatalf("Expected 400 storing an empty credential, got %d", status)
	}
}

// A run authenticates with the stored credential rather than anything in the
// environment.
func TestRunUsesTheStoredCredential(t *testing.T) {
	model := launchWithModel(t)
	defer teardown()

	apiRequest(t, http.MethodPut, "/api/secrets/default",
		map[string]string{"value": "sk-stored-1234"})

	model.queue(textResponse("Used the stored key."))

	agentID := agentUsing(t, model, "Authenticated", nil, nil)
	waitForRunStatus(t, triggerRun(t, agentID), "succeeded")

	if got := model.authorizationSeen(); got != "Bearer sk-stored-1234" {
		t.Fatalf("Expected the stored credential to be sent, got %q", got)
	}
}

// An agent may name a different stored credential, so more than one provider
// can be configured at once.
func TestAgentCanNameADifferentCredential(t *testing.T) {
	model := launchWithModel(t)
	defer teardown()

	apiRequest(t, http.MethodPut, "/api/secrets/default", map[string]string{"value": "sk-default-0000"})
	apiRequest(t, http.MethodPut, "/api/secrets/research", map[string]string{"value": "sk-research-9999"})

	model.queue(textResponse("Used the research key."))

	agentID := agentUsing(t, model, "Researcher", nil,
		map[string]interface{}{"secret_name": "research"})
	waitForRunStatus(t, triggerRun(t, agentID), "succeeded")

	if got := model.authorizationSeen(); got != "Bearer sk-research-9999" {
		t.Fatalf("Expected the named credential to be sent, got %q", got)
	}
}

// The API is closed to anyone without a session: an instance on a home network
// is otherwise open to everyone on it.
func TestAPIRequiresASession(t *testing.T) {
	cluster = Cluster{}
	cluster.LaunchAPI(apiPort)
	defer teardown()

	// Nothing sensitive is reachable without the cookie.
	for _, path := range []string{"/api/agents", "/api/secrets", "/api/tools"} {
		request, _ := http.NewRequest(http.MethodGet, "http://localhost"+apiPort+path, nil)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatalf("Request failed: %v", err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized {
			t.Errorf("Expected 401 for %s without a session, got %d", path, response.StatusCode)
		}
	}

	// The health check stays open, because an orchestrator needs it.
	response, err := http.Get("http://localhost" + apiPort + "/healthz")
	if err != nil {
		t.Fatalf("Health check failed: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Errorf("Expected the health check to stay open, got %d", response.StatusCode)
	}
}

func TestSignInAndOut(t *testing.T) {
	cluster = Cluster{}
	cluster.LaunchAPI(apiPort)
	defer teardown()

	// A wrong password does not issue a session.
	status, _ := apiRequest(t, http.MethodPost, "/api/login",
		map[string]string{"password": "not-the-password"})
	if status != http.StatusUnauthorized {
		t.Fatalf("Expected 401 for a wrong password, got %d", status)
	}

	status, body := apiRequest(t, http.MethodGet, "/api/session", nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"signed_in":true`) {
		t.Fatalf("Expected the session to report as signed in, got %d: %s", status, body)
	}

	// Signing out invalidates the session that was issued at launch.
	status, _ = apiRequest(t, http.MethodPost, "/api/logout", nil)
	if status != http.StatusOK {
		t.Fatalf("Expected 200 signing out, got %d", status)
	}

	status, _ = apiRequest(t, http.MethodGet, "/api/agents", nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("Expected 401 after signing out, got %d", status)
	}
}
