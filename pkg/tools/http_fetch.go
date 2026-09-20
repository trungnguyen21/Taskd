package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// fetchTimeout bounds a single fetch, so one unresponsive host cannot consume a
// run's whole duration budget.
const fetchTimeout = 30 * time.Second

// maxFetchBytes bounds what a fetch returns. Everything a tool returns is fed
// back into the model's context and paid for by the token.
const maxFetchBytes = 100 * 1024

// HTTPFetch reads a URL the agent names.
type HTTPFetch struct {
	client *http.Client
	policy *addressPolicy
}

// NewHTTPFetch builds the tool. Private and link-local addresses are refused
// unless the operator has allowed them.
func NewHTTPFetch(allowPrivateAddresses bool) *HTTPFetch {
	policy := &addressPolicy{allowPrivate: allowPrivateAddresses}
	return &HTTPFetch{
		policy: policy,
		client: &http.Client{
			Timeout: fetchTimeout,
			// A redirect is another chance to reach somewhere internal, so
			// every hop is checked rather than only the first.
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return fmt.Errorf("too many redirects")
				}
				return policy.check(req.URL)
			},
		},
	}
}

func (HTTPFetch) Name() string { return "http_fetch" }

func (HTTPFetch) Description() string {
	return "Fetch the contents of a URL over HTTP or HTTPS and return the response body as text."
}

func (HTTPFetch) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{
				"type":        "string",
				"description": "The absolute http:// or https:// URL to fetch.",
			},
		},
		"required": []string{"url"},
	}
}

func (h *HTTPFetch) Execute(ctx context.Context, arguments json.RawMessage, env Env) (string, error) {
	var input struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(arguments, &input); err != nil {
		return "", fmt.Errorf("arguments were not valid JSON: %w", err)
	}
	if input.URL == "" {
		return "", fmt.Errorf("a url is required")
	}

	target, err := url.Parse(input.URL)
	if err != nil {
		return "", fmt.Errorf("could not parse the url: %w", err)
	}
	if err := h.policy.check(target); err != nil {
		return "", err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("User-Agent", "Taskd")

	response, err := h.client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, maxFetchBytes))
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("HTTP %d\n\n%s", response.StatusCode, string(body)), nil
}
