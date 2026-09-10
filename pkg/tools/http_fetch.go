package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
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
	// allowPrivateAddresses opens up private ranges. Off by default, because a
	// tool that fetches arbitrary URLs from inside the deployment is otherwise
	// an SSRF engine pointed at its own network. Self-hosters who want an agent
	// to reach something on their LAN turn it on deliberately.
	allowPrivateAddresses bool
}

// NewHTTPFetch builds the tool. Private and link-local addresses are refused
// unless the operator has allowed them.
func NewHTTPFetch(allowPrivateAddresses bool) *HTTPFetch {
	fetch := &HTTPFetch{allowPrivateAddresses: allowPrivateAddresses}
	fetch.client = &http.Client{
		Timeout: fetchTimeout,
		// A redirect is another chance to reach somewhere internal, so every
		// hop is checked rather than only the first.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			return fetch.checkAddress(req.URL)
		},
	}
	return fetch
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
	if err := h.checkAddress(target); err != nil {
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

// checkAddress refuses private ranges and cloud metadata endpoints unless the
// operator has opted in.
//
// The agent driving this tool is acting on text written by whoever controls the
// page it read last, which is why the check is on the tool rather than left to
// the prompt.
func (h *HTTPFetch) checkAddress(target *url.URL) error {
	switch strings.ToLower(target.Scheme) {
	case "http", "https":
	default:
		return fmt.Errorf("only http and https urls may be fetched")
	}

	host := target.Hostname()
	if host == "" {
		return fmt.Errorf("the url has no host")
	}

	addresses, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("could not resolve %s", host)
	}

	// The cloud metadata endpoint is refused even when private addresses are
	// allowed. A self-hoster may legitimately want an agent to reach something
	// on their LAN; nobody legitimately wants one reading instance credentials.
	for _, address := range addresses {
		if isMetadataAddress(address) {
			return fmt.Errorf("refusing to fetch %s: it resolves to a cloud metadata endpoint", host)
		}
	}

	if h.allowPrivateAddresses {
		return nil
	}

	for _, address := range addresses {
		if !isPublicAddress(address) {
			return fmt.Errorf("refusing to fetch %s: it resolves to a private or link-local address", host)
		}
	}
	return nil
}

// isMetadataAddress reports the well-known instance metadata addresses used by
// the major cloud providers.
func isMetadataAddress(address net.IP) bool {
	switch address.String() {
	case "169.254.169.254", "fd00:ec2::254", "169.254.170.2":
		return true
	}
	return false
}

func isPublicAddress(address net.IP) bool {
	if address.IsLoopback() || address.IsPrivate() || address.IsUnspecified() ||
		address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() ||
		address.IsInterfaceLocalMulticast() {
		return false
	}
	return true
}
