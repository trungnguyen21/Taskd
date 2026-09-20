package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/trungnguyen21/Taskd/pkg/llm"
	"github.com/trungnguyen21/Taskd/pkg/model"
	"github.com/trungnguyen21/Taskd/pkg/store"
	"github.com/trungnguyen21/Taskd/pkg/tools"
)

// registerAgentRoutes wires the dashboard-facing agent endpoints. These are
// answered straight from Postgres: the coordinator is a participant in the
// database, not a gatekeeper in front of it.
func (s *SchedulerServer) registerAgentRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/agents", s.handleListAgents)
	mux.HandleFunc("POST /api/agents", s.handleCreateAgent)
	mux.HandleFunc("GET /api/agents/{id}", s.handleGetAgent)
	mux.HandleFunc("PATCH /api/agents/{id}", s.handleUpdateAgent)
	mux.HandleFunc("DELETE /api/agents/{id}", s.handleDeleteAgent)
	mux.HandleFunc("GET /api/tools", s.handleListTools)
	mux.HandleFunc("GET /api/models", s.handleListModels)
	s.registerMemoryRoutes(mux)
}

// handleListAgents answers the dashboard's home screen in one request.
//
// Each row carries the agent's schedule and its last run, because the question
// the screen exists to answer - is anything broken - cannot be answered from
// the agent rows alone, and asking per agent would be a request per row.
func (s *SchedulerServer) handleListAgents(w http.ResponseWriter, r *http.Request) {
	agents, err := s.agents.ListOverview(r.Context(), model.OwnerUserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, agents)
}

func (s *SchedulerServer) handleCreateAgent(w http.ResponseWriter, r *http.Request) {
	agent := &model.Agent{Enabled: true}
	if !decodeBody(w, r, agent) {
		return
	}

	agent.ApplyDefaults()
	if err := agent.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.validateAgentModel(r.Context(), agent); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	created, err := s.agents.Create(r.Context(), model.OwnerUserID, agent)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *SchedulerServer) handleGetAgent(w http.ResponseWriter, r *http.Request) {
	agent, err := s.agents.Get(r.Context(), model.OwnerUserID, r.PathValue("id"))
	if writeStoreError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, agent)
}

func (s *SchedulerServer) handleUpdateAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// Decoding onto the stored agent leaves fields the caller omitted untouched,
	// which is what makes this a patch rather than a replace.
	agent, err := s.agents.Get(r.Context(), model.OwnerUserID, id)
	if writeStoreError(w, err) {
		return
	}
	if !decodeBody(w, r, agent) {
		return
	}

	agent.ApplyDefaults()
	if err := agent.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.validateAgentModel(r.Context(), agent); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	updated, err := s.agents.Update(r.Context(), model.OwnerUserID, id, agent)
	if writeStoreError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *SchedulerServer) handleDeleteAgent(w http.ResponseWriter, r *http.Request) {
	err := s.agents.Delete(r.Context(), model.OwnerUserID, r.PathValue("id"))
	if writeStoreError(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleListTools returns the tools this installation can actually offer.
//
// A tool whose credential is not configured is absent rather than listed and
// broken: a catalogue that offers something which fails the moment an agent
// calls it is worse than one that is honest about what is available here.
func (s *SchedulerServer) handleListTools(w http.ResponseWriter, r *http.Request) {
	type toolSummary struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}

	summaries := []toolSummary{}
	for _, name := range s.registry.Names() {
		tool, ok := s.registry.Get(name)
		if !ok {
			continue
		}
		// Some tools depend on settings the user edits while the process runs,
		// so availability is decided per request rather than at startup.
		if !tools.Available(r.Context(), tool, model.OwnerUserID) {
			continue
		}
		summaries = append(summaries, toolSummary{Name: tool.Name(), Description: tool.Description()})
	}
	writeJSON(w, http.StatusOK, summaries)
}

func (s *SchedulerServer) handleListModels(w http.ResponseWriter, r *http.Request) {
	predefined := []model.ModelEndpoint{
		{Name: "GPT-4o (OpenAI)", Model: "gpt-4o", BaseURL: "https://api.openai.com/v1", SecretName: "openai_key"},
		{Name: "GPT-4o Mini (OpenAI)", Model: "gpt-4o-mini", BaseURL: "https://api.openai.com/v1", SecretName: "openai_key"},
		{Name: "GPT-4 Turbo (OpenAI)", Model: "gpt-4-turbo", BaseURL: "https://api.openai.com/v1", SecretName: "openai_key"},
		{Name: "Gemini 1.5 Pro (Google)", Model: "gemini-1.5-pro", BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai", SecretName: "google_key"},
		{Name: "Gemini 1.5 Flash (Google)", Model: "gemini-1.5-flash", BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai", SecretName: "google_key"},
		{Name: "Gemini 1.0 Pro (Google)", Model: "gemini-1.0-pro", BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai", SecretName: "google_key"},
		{Name: "Claude Sonnet 5 (Anthropic)", Model: "claude-sonnet-5", BaseURL: "https://api.anthropic.com/v1", SecretName: "anthropic_key"},
		{Name: "Claude Opus 5 (Anthropic)", Model: "claude-opus-5", BaseURL: "https://api.anthropic.com/v1", SecretName: "anthropic_key"},
		{Name: "Claude Haiku 4.5 (Anthropic)", Model: "claude-haiku-4-5", BaseURL: "https://api.anthropic.com/v1", SecretName: "anthropic_key"},
	}

	settings, err := s.settings.Get(r.Context(), model.OwnerUserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	secrets, err := s.secrets.List(r.Context(), model.OwnerUserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	
	secretNames := make(map[string]bool)
	for _, sec := range secrets {
		secretNames[sec.Name] = true
	}

	var results []model.ModelEndpoint
	for _, pre := range predefined {
		if secretNames[pre.SecretName] {
			results = append(results, pre)
		}
	}

	if settings.CustomProviders != nil {
		for _, cp := range settings.CustomProviders {
			for _, m := range cp.Models {
				results = append(results, model.ModelEndpoint{
					Name:       m + " (" + cp.Name + ")",
					Model:      m,
					BaseURL:    cp.BaseURL,
					SecretName: cp.SecretName,
				})
			}
		}
	}

	writeJSON(w, http.StatusOK, results)
}

func (s *SchedulerServer) validateAgentModel(ctx context.Context, agent *model.Agent) error {
	if os.Getenv("TASKD_SKIP_VALIDATION") == "true" {
		return nil
	}

	apiKey := ""
	if agent.SecretName != "" && agent.SecretName != model.DefaultSecretName {
		val, err := s.secrets.Reveal(ctx, model.OwnerUserID, agent.SecretName)
		if err != nil {
			return fmt.Errorf("invalid credential %q: %v", agent.SecretName, err)
		}
		apiKey = val
	} else {
		apiKey = os.Getenv("TASKD_MODEL_API_KEY")
	}

	client := llm.New(agent.BaseURL, apiKey)
	_, err := client.Complete(ctx, llm.Request{
		Model:    agent.Model,
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "respond with 'ok'"}},
	})
	if err != nil {
		return fmt.Errorf("provider validation failed: %v", err)
	}
	return nil
}

// registerMemoryRoutes exposes what agents have remembered, so a user can see
// and correct it.
func (s *SchedulerServer) registerMemoryRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/agents/{id}/memory", s.handleListMemory)
	mux.HandleFunc("DELETE /api/memory/{id}", s.handleDeleteMemory)
}

func (s *SchedulerServer) handleListMemory(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	if _, err := s.agents.Get(r.Context(), model.OwnerUserID, agentID); writeStoreError(w, err) {
		return
	}

	records, err := s.memory.ListByAgent(r.Context(), model.OwnerUserID, agentID, store.MaxRecordsPerNamespace)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, records)
}

func (s *SchedulerServer) handleDeleteMemory(w http.ResponseWriter, r *http.Request) {
	err := s.memory.Delete(r.Context(), model.OwnerUserID, r.PathValue("id"))
	if writeStoreError(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func decodeBody(w http.ResponseWriter, r *http.Request, target interface{}) bool {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return false
	}
	if err := json.Unmarshal(body, target); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return false
	}
	return true
}

// writeStoreError reports whether it handled the error.
func writeStoreError(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
