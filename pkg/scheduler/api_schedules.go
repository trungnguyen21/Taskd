package scheduler

import (
	"net/http"
	"time"

	"github.com/JyotinderSingh/task-queue/pkg/model"
	"github.com/JyotinderSingh/task-queue/pkg/schedule"
)

// defaultRunListLimit bounds the run history a single request returns.
const defaultRunListLimit = 100

// previewFireCount is how many upcoming fire times the schedule picker shows,
// so a user can confirm a schedule before saving it.
const previewFireCount = 5

func (s *SchedulerServer) registerScheduleRoutes(mux *http.ServeMux) {
	mux.HandleFunc("PUT /api/agents/{id}/schedule", s.handleSetSchedule)
	mux.HandleFunc("GET /api/agents/{id}/schedule", s.handleGetSchedule)
	mux.HandleFunc("DELETE /api/agents/{id}/schedule", s.handleDeleteSchedule)
	mux.HandleFunc("POST /api/schedules/preview", s.handlePreviewSchedule)

	mux.HandleFunc("GET /api/agents/{id}/runs", s.handleListRuns)
	mux.HandleFunc("POST /api/agents/{id}/runs", s.handleTriggerRun)
	mux.HandleFunc("GET /api/runs/{id}", s.handleGetRun)
	mux.HandleFunc("GET /api/runs/{id}/steps", s.handleListRunSteps)
}

func (s *SchedulerServer) handleSetSchedule(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	if _, err := s.agents.Get(r.Context(), model.OwnerUserID, agentID); writeStoreError(w, err) {
		return
	}

	requested := &model.Schedule{Enabled: true}
	if !decodeBody(w, r, requested) {
		return
	}

	spec, err := schedule.Parse(requested.CronExpression, requested.Timezone)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// A schedule starts from now, so saving one never fires it retroactively.
	nextFireAt := spec.Next(s.clock.Now(), "")
	if nextFireAt.IsZero() {
		writeError(w, http.StatusBadRequest, "schedule has no upcoming fire time")
		return
	}

	stored, err := s.schedules.Upsert(r.Context(), model.OwnerUserID, agentID, requested,
		nextFireAt, "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, stored)
}

func (s *SchedulerServer) handleGetSchedule(w http.ResponseWriter, r *http.Request) {
	stored, err := s.schedules.GetByAgent(r.Context(), model.OwnerUserID, r.PathValue("id"))
	if writeStoreError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, stored)
}

func (s *SchedulerServer) handleDeleteSchedule(w http.ResponseWriter, r *http.Request) {
	err := s.schedules.DeleteByAgent(r.Context(), model.OwnerUserID, r.PathValue("id"))
	if writeStoreError(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handlePreviewSchedule answers "when would this actually run", so a schedule
// can be confirmed before it is saved.
func (s *SchedulerServer) handlePreviewSchedule(w http.ResponseWriter, r *http.Request) {
	var requested struct {
		CronExpression string `json:"cron_expression"`
		Timezone       string `json:"timezone"`
	}
	if !decodeBody(w, r, &requested) {
		return
	}

	spec, err := schedule.Parse(requested.CronExpression, requested.Timezone)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	fireTimes := []time.Time{}
	at := s.clock.Now()
	for i := 0; i < previewFireCount; i++ {
		at = spec.Next(at, "")
		if at.IsZero() {
			break
		}
		fireTimes = append(fireTimes, at)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"fire_times": fireTimes})
}

func (s *SchedulerServer) handleListRuns(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	if _, err := s.agents.Get(r.Context(), model.OwnerUserID, agentID); writeStoreError(w, err) {
		return
	}

	runs, err := s.runs.ListByAgent(r.Context(), model.OwnerUserID, agentID, defaultRunListLimit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

// handleTriggerRun queues a run immediately, so an agent can be tested without
// waiting for its schedule.
func (s *SchedulerServer) handleTriggerRun(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	agent, err := s.agents.Get(r.Context(), model.OwnerUserID, agentID)
	if writeStoreError(w, err) {
		return
	}

	run, err := s.runs.Create(r.Context(), model.OwnerUserID, agent.ID, nil,
		model.TriggerManual, model.RunPending, s.clock.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, run)
}

func (s *SchedulerServer) handleGetRun(w http.ResponseWriter, r *http.Request) {
	run, err := s.runs.Get(r.Context(), model.OwnerUserID, r.PathValue("id"))
	if writeStoreError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, run)
}

// handleListRunSteps returns a run's trace: every model call and tool call in
// order, with arguments and results. An unattended agent is only trustworthy if
// what it did can be read back afterwards.
func (s *SchedulerServer) handleListRunSteps(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("id")
	if _, err := s.runs.Get(r.Context(), model.OwnerUserID, runID); writeStoreError(w, err) {
		return
	}

	steps, err := s.steps.ListByRun(r.Context(), model.OwnerUserID, runID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, steps)
}
