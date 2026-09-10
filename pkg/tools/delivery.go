package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// telegramMessageLimit is Telegram's own cap on a message. Digests routinely
// exceed it, so output is split rather than truncated.
const telegramMessageLimit = 4096

// DefaultTelegramBaseURL is the real Telegram API.
const DefaultTelegramBaseURL = "https://api.telegram.org"

const deliveryTimeout = 30 * time.Second

// TelegramSettings resolves a user's bot token and chat id.
//
// It is resolved per call rather than at startup, because the operator edits
// these in the dashboard and a tool built once at boot would keep sending to
// wherever it was pointed when the process started.
type TelegramSettings interface {
	TelegramDelivery(ctx context.Context, userID string) (token, chatID string, ok bool)
}

// SendTelegram delivers text to the operator's Telegram chat.
//
// Delivery is a tool the model calls rather than a property of the schedule,
// which is what makes "only tell me if something actually changed" expressible.
type SendTelegram struct {
	baseURL  string
	settings TelegramSettings
	client   *http.Client
}

func NewSendTelegram(baseURL string, settings TelegramSettings) *SendTelegram {
	if baseURL == "" {
		baseURL = DefaultTelegramBaseURL
	}
	return &SendTelegram{
		baseURL:  strings.TrimSuffix(baseURL, "/"),
		settings: settings,
		client:   &http.Client{Timeout: deliveryTimeout},
	}
}

// Configured reports whether this user can actually be reached, so the
// catalogue does not offer a tool that would fail the moment it is called.
func (s *SendTelegram) Configured(ctx context.Context, userID string) bool {
	_, _, ok := s.settings.TelegramDelivery(ctx, userID)
	return ok
}

func (SendTelegram) Name() string { return "send_telegram" }

func (SendTelegram) Description() string {
	return "Send a message to the user's Telegram. Use this to deliver your result to them."
}

func (SendTelegram) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"text": map[string]any{
				"type":        "string",
				"description": "The message to send. Plain text.",
			},
		},
		"required": []string{"text"},
	}
}

func (s *SendTelegram) Execute(ctx context.Context, arguments json.RawMessage, env Env) (string, error) {
	var input struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(arguments, &input); err != nil {
		return "", fmt.Errorf("arguments were not valid JSON: %w", err)
	}
	if strings.TrimSpace(input.Text) == "" {
		return "", fmt.Errorf("text is required")
	}

	token, chatID, ok := s.settings.TelegramDelivery(ctx, env.UserID)
	if !ok {
		return "", fmt.Errorf("Telegram is not configured: set a bot token and chat id in settings")
	}

	chunks := splitForTelegram(input.Text)
	for _, chunk := range chunks {
		// Plain text is the default: model output is full of characters that
		// Telegram's markdown modes require escaping, and an unescaped one is a
		// failed send rather than an ugly message.
		body, err := json.Marshal(map[string]string{"chat_id": chatID, "text": chunk})
		if err != nil {
			return "", err
		}

		request, err := http.NewRequestWithContext(ctx, http.MethodPost,
			fmt.Sprintf("%s/bot%s/sendMessage", s.baseURL, token), bytes.NewReader(body))
		if err != nil {
			return "", err
		}
		request.Header.Set("Content-Type", "application/json")

		response, err := s.client.Do(request)
		if err != nil {
			return "", err
		}
		responseBody, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		response.Body.Close()

		if response.StatusCode != http.StatusOK {
			return "", fmt.Errorf("telegram returned %d: %s",
				response.StatusCode, strings.TrimSpace(string(responseBody)))
		}
	}

	if len(chunks) == 1 {
		return "Message sent.", nil
	}
	return fmt.Sprintf("Message sent in %d parts.", len(chunks)), nil
}

// splitForTelegram breaks text on paragraph boundaries where it can, and mid-line
// only when a single line is itself too long.
func splitForTelegram(text string) []string {
	if len(text) <= telegramMessageLimit {
		return []string{text}
	}

	chunks := []string{}
	current := strings.Builder{}

	for _, paragraph := range strings.Split(text, "\n") {
		for len(paragraph) > telegramMessageLimit {
			if current.Len() > 0 {
				chunks = append(chunks, current.String())
				current.Reset()
			}
			chunks = append(chunks, paragraph[:telegramMessageLimit])
			paragraph = paragraph[telegramMessageLimit:]
		}

		if current.Len()+len(paragraph)+1 > telegramMessageLimit {
			chunks = append(chunks, current.String())
			current.Reset()
		}
		if current.Len() > 0 {
			current.WriteString("\n")
		}
		current.WriteString(paragraph)
	}

	if current.Len() > 0 {
		chunks = append(chunks, current.String())
	}
	return chunks
}

// SendWebhook posts to a URL the agent names. It is the universal escape hatch:
// Discord, ntfy, Home Assistant and anything else, without an integration each.
type SendWebhook struct {
	policy *addressPolicy
	client *http.Client
}

func NewSendWebhook(allowPrivateAddresses bool) *SendWebhook {
	policy := &addressPolicy{allowPrivate: allowPrivateAddresses}
	return &SendWebhook{
		policy: policy,
		client: &http.Client{
			Timeout: deliveryTimeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return fmt.Errorf("too many redirects")
				}
				return policy.check(req.URL)
			},
		},
	}
}

func (SendWebhook) Name() string { return "send_webhook" }

func (SendWebhook) Description() string {
	return "POST a JSON payload to a URL. Use this to deliver a result to Discord, ntfy, " +
		"Home Assistant or any other service that accepts a webhook."
}

func (SendWebhook) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{
				"type":        "string",
				"description": "The absolute https:// URL to post to.",
			},
			"payload": map[string]any{
				"description": "The JSON body to send.",
			},
		},
		"required": []string{"url", "payload"},
	}
}

func (s *SendWebhook) Execute(ctx context.Context, arguments json.RawMessage, env Env) (string, error) {
	var input struct {
		URL     string          `json:"url"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(arguments, &input); err != nil {
		return "", fmt.Errorf("arguments were not valid JSON: %w", err)
	}
	if input.URL == "" {
		return "", fmt.Errorf("a url is required")
	}
	if len(input.Payload) == 0 {
		input.Payload = json.RawMessage(`{}`)
	}

	target, err := url.Parse(input.URL)
	if err != nil {
		return "", fmt.Errorf("could not parse the url: %w", err)
	}
	if err := s.policy.check(target); err != nil {
		return "", err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		target.String(), bytes.NewReader(input.Payload))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := s.client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()

	if response.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
		return "", fmt.Errorf("the webhook returned %d: %s",
			response.StatusCode, strings.TrimSpace(string(body)))
	}
	return fmt.Sprintf("Posted successfully (HTTP %d).", response.StatusCode), nil
}
