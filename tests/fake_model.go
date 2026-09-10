package tests

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"

	"github.com/JyotinderSingh/task-queue/pkg/llm"
)

// fakeModel is an OpenAI-compatible endpoint that returns scripted responses.
//
// No mock object is involved: agents already take a custom base URL so that
// Ollama and other compatible servers work, so the product's own
// configurability is the test seam. The executor exercises its real HTTP path.
type fakeModel struct {
	server *httptest.Server

	mu            sync.Mutex
	queued        []llm.Response
	fallback      *llm.Response
	failWith      int
	requests      []llm.Request
	callCount     int
	authorization string
}

func newFakeModel() *fakeModel {
	model := &fakeModel{}
	model.server = httptest.NewServer(http.HandlerFunc(model.handle))
	return model
}

func (f *fakeModel) close() {
	f.server.Close()
}

// baseURL is what an agent's base_url is set to.
func (f *fakeModel) baseURL() string {
	return f.server.URL
}

// queue adds one scripted response, consumed in order.
func (f *fakeModel) queue(response llm.Response) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queued = append(f.queued, response)
}

// always makes every call return the same response, for loops that must be
// stopped by a budget rather than by the model finishing.
func (f *fakeModel) always(response llm.Response) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fallback = &response
}

// failWithStatus makes the endpoint reject calls, standing in for a bad key or
// an unreachable provider.
func (f *fakeModel) failWithStatus(status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failWith = status
}

// requestsReceived returns what the executor actually sent.
func (f *fakeModel) requestsReceived() []llm.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]llm.Request(nil), f.requests...)
}

func (f *fakeModel) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.callCount
}

func (f *fakeModel) handle(w http.ResponseWriter, r *http.Request) {
	var request llm.Request
	json.NewDecoder(r.Body).Decode(&request)

	f.mu.Lock()
	f.requests = append(f.requests, request)
	f.callCount++
	f.authorization = r.Header.Get("Authorization")

	if f.failWith != 0 {
		status := f.failWith
		f.mu.Unlock()
		http.Error(w, `{"error":{"message":"invalid api key"}}`, status)
		return
	}

	var response llm.Response
	switch {
	case len(f.queued) > 0:
		response = f.queued[0]
		f.queued = f.queued[1:]
	case f.fallback != nil:
		response = *f.fallback
	default:
		response = textResponse("no scripted response remained")
	}
	f.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// textResponse is a model turn that answers instead of calling a tool.
func textResponse(content string) llm.Response {
	return llm.Response{
		Choices: []llm.Choice{{
			Message:      llm.Message{Role: llm.RoleAssistant, Content: content},
			FinishReason: "stop",
		}},
		Usage: llm.Usage{PromptTokens: 11, CompletionTokens: 7, TotalTokens: 18},
	}
}

// toolCallResponse is a model turn that asks for a tool.
func toolCallResponse(id, name, arguments string) llm.Response {
	return llm.Response{
		Choices: []llm.Choice{{
			Message: llm.Message{
				Role: llm.RoleAssistant,
				ToolCalls: []llm.ToolCall{{
					ID:       id,
					Type:     "function",
					Function: llm.FunctionCall{Name: name, Arguments: arguments},
				}},
			},
			FinishReason: "tool_calls",
		}},
		Usage: llm.Usage{PromptTokens: 13, CompletionTokens: 5, TotalTokens: 18},
	}
}

// authorizationSeen returns the credential the executor presented on the most
// recent call.
func (f *fakeModel) authorizationSeen() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.authorization
}
