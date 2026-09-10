package scheduler

import (
	"net/http"
	"strings"

	"github.com/JyotinderSingh/task-queue/pkg/model"
	"github.com/JyotinderSingh/task-queue/pkg/store"
)

// sessionCookieName is the cookie the dashboard carries.
const sessionCookieName = "taskd_session"

func (s *SchedulerServer) registerAuthRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("POST /api/logout", s.handleLogout)
	mux.HandleFunc("GET /api/session", s.handleSession)
}

// requireSession closes the API to anyone without a live session.
//
// An instance exposed to a home network is otherwise open to everyone on it,
// and the credentials it holds are the point of the exercise.
func (s *SchedulerServer) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isPublicPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		cookie, err := r.Cookie(sessionCookieName)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "not signed in")
			return
		}

		if _, err := s.users.UserForSession(r.Context(), cookie.Value, s.clock.Now()); err != nil {
			writeError(w, http.StatusUnauthorized, "not signed in")
			return
		}

		next.ServeHTTP(w, r)
	})
}

// isPublicPath lists what may be reached without signing in: the health check
// an orchestrator needs, and the login endpoint itself.
func isPublicPath(path string) bool {
	switch {
	case path == "/healthz":
		return true
	case path == "/api/login":
		return true
	case path == "/api/session":
		return true
	case !strings.HasPrefix(path, "/api/"):
		// Static dashboard assets. The API behind them is still closed.
		return true
	}
	return false
}

func (s *SchedulerServer) handleLogin(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Password string `json:"password"`
	}
	if !decodeBody(w, r, &request) {
		return
	}

	ok, err := s.users.CheckPassword(r.Context(), model.OwnerUserID, request.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusUnauthorized, "incorrect password")
		return
	}

	token, err := s.users.CreateSession(r.Context(), model.OwnerUserID, s.clock.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(store.SessionLifetime.Seconds()),
	})
	writeJSON(w, http.StatusOK, map[string]bool{"signed_in": true})
}

func (s *SchedulerServer) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		s.users.DeleteSession(r.Context(), cookie.Value)
	}

	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: "", Path: "/", HttpOnly: true, MaxAge: -1,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"signed_in": false})
}

// handleSession tells the dashboard whether it needs to show a login form.
func (s *SchedulerServer) handleSession(w http.ResponseWriter, r *http.Request) {
	signedIn := false
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		if _, err := s.users.UserForSession(r.Context(), cookie.Value, s.clock.Now()); err == nil {
			signedIn = true
		}
	}
	writeJSON(w, http.StatusOK, map[string]bool{"signed_in": signedIn})
}

// registerSecretRoutes exposes stored credentials. Values go in and are never
// returned: the dashboard shows which key is stored, not the key.
func (s *SchedulerServer) registerSecretRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/secrets", s.handleListSecrets)
	mux.HandleFunc("PUT /api/secrets/{name}", s.handlePutSecret)
	mux.HandleFunc("DELETE /api/secrets/{name}", s.handleDeleteSecret)
}

func (s *SchedulerServer) handleListSecrets(w http.ResponseWriter, r *http.Request) {
	secrets, err := s.secrets.List(r.Context(), model.OwnerUserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, secrets)
}

func (s *SchedulerServer) handlePutSecret(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Value string `json:"value"`
	}
	if !decodeBody(w, r, &request) {
		return
	}
	if strings.TrimSpace(request.Value) == "" {
		writeError(w, http.StatusBadRequest, "a value is required")
		return
	}

	secret, err := s.secrets.Put(r.Context(), model.OwnerUserID, r.PathValue("name"), request.Value)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, secret)
}

func (s *SchedulerServer) handleDeleteSecret(w http.ResponseWriter, r *http.Request) {
	if err := s.secrets.Delete(r.Context(), model.OwnerUserID, r.PathValue("name")); writeStoreError(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
