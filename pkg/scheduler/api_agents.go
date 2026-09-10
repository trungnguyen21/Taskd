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
		summaries = append(summaries, toolSummary{Name: tool.Name(), Description: tool.Description()})
	}
	writeJSON(w, http.StatusOK, summaries)
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
