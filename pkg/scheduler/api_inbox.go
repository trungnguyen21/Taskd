package scheduler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/JyotinderSingh/task-queue/pkg/model"
)

// inboxLimit bounds how much history one request returns.
const inboxLimit = 100

func (s *SchedulerServer) registerInboxRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/inbox", s.handleInbox)
	mux.HandleFunc("POST /api/runs/{id}/read", s.handleMarkRead)

	mux.HandleFunc("GET /api/settings", s.handleGetSettings)
	mux.HandleFunc("PUT /api/settings", s.handleUpdateSettings)
	mux.HandleFunc("POST /api/telegram/detect-chat", s.handleDetectChat)
}

// handleInbox returns what agents have produced, newest first.
func (s *SchedulerServer) handleInbox(w http.ResponseWriter, r *http.Request) {
	items, unread, err := s.runs.Inbox(r.Context(), model.OwnerUserID, inboxLimit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"items":        items,
		"unread_count": unread,
	})
}

func (s *SchedulerServer) handleMarkRead(w http.ResponseWriter, r *http.Request) {
	if err := s.runs.MarkRead(r.Context(), model.OwnerUserID,
		r.PathValue("id"), s.clock.Now()); writeStoreError(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *SchedulerServer) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.settings.Get(r.Context(), model.OwnerUserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *SchedulerServer) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.settings.Get(r.Context(), model.OwnerUserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !decodeBody(w, r, settings) {
		return
	}

	updated, err := s.settings.Update(r.Context(), model.OwnerUserID, settings)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// handleDetectChat finds the chat id of whoever last messaged the bot.
//
// The alternative is telling the operator to call getUpdates by hand and read a
// number out of the JSON, which is the step that generates the support
// questions.
func (s *SchedulerServer) handleDetectChat(w http.ResponseWriter, r *http.Request) {
	token, err := s.settings.TelegramToken(r.Context(), model.OwnerUserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if token == "" {
		writeError(w, http.StatusBadRequest,
			"store a bot token first, then send /start to your bot")
		return
	}

	baseURL := s.telegramBaseURL()
	response, err := http.Get(fmt.Sprintf("%s/bot%s/getUpdates", baseURL, token))
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if response.StatusCode != http.StatusOK {
		writeError(w, http.StatusBadGateway,
			fmt.Sprintf("telegram returned %d: %s", response.StatusCode, strings.TrimSpace(string(body))))
		return
	}

	var updates struct {
		Result []struct {
			Message struct {
				Chat struct {
					ID int64 `json:"id"`
				} `json:"chat"`
			} `json:"message"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &updates); err != nil {
		writeError(w, http.StatusBadGateway, "could not decode the Telegram response")
		return
	}

	chatID := int64(0)
	for _, update := range updates.Result {
		if update.Message.Chat.ID != 0 {
			chatID = update.Message.Chat.ID
		}
	}
	if chatID == 0 {
		writeError(w, http.StatusNotFound,
			"no messages found: send /start to your bot, then try again")
		return
	}

	settings, err := s.settings.Get(r.Context(), model.OwnerUserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	settings.TelegramChatID = fmt.Sprintf("%d", chatID)

	updated, err := s.settings.Update(r.Context(), model.OwnerUserID, settings)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// telegramBaseURL lets tests point the discovery flow at a stand-in.
func (s *SchedulerServer) telegramBaseURL() string {
	if configured := s.toolConfig.TelegramBaseURL; configured != "" {
		return strings.TrimSuffix(configured, "/")
	}
	return "https://api.telegram.org"
}
