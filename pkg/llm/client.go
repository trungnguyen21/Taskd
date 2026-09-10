// Package llm speaks one wire format: OpenAI-compatible chat completions with
// tool calling.
//
// This is not an endorsement of a provider. It is the de facto interchange
// format, so a single request shape reaches OpenAI, Ollama, LM Studio, vLLM and
// OpenRouter directly, and Anthropic and Gemini through their compatibility
// endpoints. The alternative - a native adapter per provider - means three or
// more tool-calling dialects to keep current as each evolves independently.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultBaseURL is used when an agent does not name one.
const DefaultBaseURL = "https://api.openai.com/v1"

// requestTimeout bounds a single model call. A run's own duration budget is
// enforced separately, across the whole loop.
const requestTimeout = 5 * time.Minute

// Roles in a conversation.
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// Message is one turn.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// ToolCall is the model asking for a tool to be run.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// FunctionCall carries the tool name and its arguments. Arguments are a JSON
// string rather than a structure, because a model is perfectly capable of
// producing something that is not valid JSON.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolDefinition declares a tool to the model.
type ToolDefinition struct {
	Type     string         `json:"type"`
	Function FunctionSchema `json:"function"`
}

// FunctionSchema is the tool's name, description and JSON Schema input.
type FunctionSchema struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// Request is a chat completion request.
type Request struct {
	Model    string           `json:"model"`
	Messages []Message        `json:"messages"`
	Tools    []ToolDefinition `json:"tools,omitempty"`
}

// Response is a chat completion response.
type Response struct {
	Choices []Choice `json:"choices"`
	Usage   Usage    `json:"usage"`
}

// Choice is one candidate completion.
type Choice struct {
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

// Usage is what the provider reports about token consumption. Tokens are
// recorded rather than converted to money: pricing needs a table maintained by
// hand, which goes stale silently.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// Client calls an OpenAI-compatible endpoint.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// New builds a client. An empty base URL means the default provider; an empty
// key is allowed, because a local model server usually wants none.
func New(baseURL, apiKey string) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		baseURL:    strings.TrimSuffix(baseURL, "/"),
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: requestTimeout},
	}
}

// Complete performs one chat completion.
func (c *Client) Complete(ctx context.Context, request Request) (*Response, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	httpResponse, err := c.httpClient.Do(httpRequest)
	if err != nil {
		return nil, err
	}
	defer httpResponse.Body.Close()

	responseBody, err := io.ReadAll(httpResponse.Body)
	if err != nil {
		return nil, err
	}

	if httpResponse.StatusCode != http.StatusOK {
		// The provider's own message is the useful part here: an invalid key
		// and an unknown model look identical without it.
		return nil, fmt.Errorf("model provider returned %d: %s",
			httpResponse.StatusCode, strings.TrimSpace(string(responseBody)))
	}

	var response Response
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return nil, fmt.Errorf("could not decode the model response: %w", err)
	}
	if len(response.Choices) == 0 {
		return nil, fmt.Errorf("the model returned no choices")
	}
	return &response, nil
}
