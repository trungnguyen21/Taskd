package scheduler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/JyotinderSingh/task-queue/pkg/model"
	"github.com/JyotinderSingh/task-queue/pkg/store"
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
}

func (s *SchedulerServer) handleListAgents(w http.ResponseWriter, r *http.Request) {
	agents, err := s.agents.List(r.Context(), model.OwnerUserID)
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

func (s *SchedulerServer) handleListTools(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, model.ToolCatalog)
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
		writeError(w, http.StatusNotFound, "agent not found")
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
