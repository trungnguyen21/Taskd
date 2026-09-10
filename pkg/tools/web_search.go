package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultSearchBaseURL is Brave's search API. Every credible search API needs a
// key, so this tool is only registered when the operator has supplied one -
// rather than appearing in the catalogue and failing when an agent calls it.
const DefaultSearchBaseURL = "https://api.search.brave.com/res/v1"

const searchTimeout = 30 * time.Second

// maxSearchResults bounds what one search returns. Everything a tool returns is
// fed back into the model's context and paid for by the token.
const maxSearchResults = 10

// WebSearch searches the web.
type WebSearch struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

func NewWebSearch(baseURL, apiKey string) *WebSearch {
	if baseURL == "" {
		baseURL = DefaultSearchBaseURL
	}
	return &WebSearch{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		apiKey:  apiKey,
		client:  &http.Client{Timeout: searchTimeout},
	}
}

func (WebSearch) Name() string { return "web_search" }

func (WebSearch) Description() string {
	return "Search the web and return the top results with their titles, URLs and descriptions."
}

func (WebSearch) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "What to search for.",
			},
			"count": map[string]any{
				"type":        "integer",
				"description": fmt.Sprintf("How many results to return, at most %d.", maxSearchResults),
			},
		},
		"required": []string{"query"},
	}
}

// searchResponse is the shape Brave returns, and what a compatible endpoint is
// expected to produce.
type searchResponse struct {
	Web struct {
		Results []struct {
			Title       string `json:"title"`
			URL         string `json:"url"`
			Description string `json:"description"`
		} `json:"results"`
	} `json:"web"`
}

func (s *WebSearch) Execute(ctx context.Context, arguments json.RawMessage, env Env) (string, error) {
	var input struct {
		Query string `json:"query"`
		Count int    `json:"count"`
	}
	if err := json.Unmarshal(arguments, &input); err != nil {
		return "", fmt.Errorf("arguments were not valid JSON: %w", err)
	}
	if strings.TrimSpace(input.Query) == "" {
		return "", fmt.Errorf("a query is required")
	}
	if input.Count < 1 || input.Count > maxSearchResults {
		input.Count = maxSearchResults
	}

	endpoint := fmt.Sprintf("%s/web/search?q=%s&count=%s", s.baseURL,
		url.QueryEscape(input.Query), strconv.Itoa(input.Count))

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-Subscription-Token", s.apiKey)

	response, err := s.client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, 512*1024))
	if err != nil {
		return "", err
	}
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("the search provider returned %d: %s",
			response.StatusCode, strings.TrimSpace(string(body)))
	}

	var decoded searchResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return "", fmt.Errorf("could not decode the search response: %w", err)
	}
	if len(decoded.Web.Results) == 0 {
		return "No results found.", nil
	}

	var builder strings.Builder
	for i, result := range decoded.Web.Results {
		if i >= input.Count {
			break
		}
		fmt.Fprintf(&builder, "%d. %s\n   %s\n   %s\n", i+1,
			result.Title, result.URL, result.Description)
	}
	return strings.TrimRight(builder.String(), "\n"), nil
}
