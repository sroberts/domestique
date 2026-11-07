// Package coach writes the day's readiness call in a director sportif's
// voice, using the LLM the Outpost operator chose (service spec §15) when
// there is one. AI is an enhancement: every caller keeps a rule-based answer
// and uses it whenever the model is missing, slow, refused or wrong.
package coach

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// AI calls Outpost's model endpoint. The zero value is unusable; build it
// with FromEnv.
type AI struct {
	URL   string // OUTPOST_AI_URL, no trailing slash
	Token string // OUTPOST_AI_TOKEN; never logged, sent only to URL
	HTTP  *http.Client
}

// FromEnv returns the operator's models, or nil when Outpost gave none (an
// older Outpost, or none at all): AI is then off.
func FromEnv() *AI {
	u := strings.TrimRight(strings.TrimSpace(os.Getenv("OUTPOST_AI_URL")), "/")
	t := strings.TrimSpace(os.Getenv("OUTPOST_AI_TOKEN"))
	if u == "" || t == "" {
		return nil
	}
	// Outpost waits up to 10 minutes for a provider to start answering, and
	// Ollama may be loading the model first.
	return &AI{URL: u, Token: t, HTTP: &http.Client{Timeout: 11 * time.Minute}}
}

// StatusError is a refusal from Outpost or the provider.
type StatusError struct {
	Status  int
	Message string
}

func (e *StatusError) Error() string { return fmt.Sprintf("ai: %d %s", e.Status, e.Message) }

// Unavailable reports whether the error means AI is off for now (no model,
// not given to this service, provider down), as opposed to a bad request.
func Unavailable(err error) bool {
	var se *StatusError
	if !errors.As(err, &se) {
		return true // network: the daemon is restarting or gone
	}
	switch se.Status {
	case http.StatusForbidden, http.StatusServiceUnavailable, http.StatusBadGateway, http.StatusTooManyRequests:
		return true
	}
	return false
}

type chatMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Chat sends one system and user message to the LLM over the OpenAI Chat
// Completions API, which every provider behind Outpost accepts, and returns
// the reply's text and the model that answered.
func (a *AI) Chat(ctx context.Context, system, user string) (text, model string, err error) {
	body, _ := json.Marshal(map[string]any{
		"model": "llm", // Outpost puts the operator's model here
		"messages": []chatMsg{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.URL+"/openai/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.Token)
	resp, err := a.HTTP.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("ai: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", "", fmt.Errorf("ai: read reply: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", &StatusError{Status: resp.StatusCode, Message: errMessage(raw)}
	}
	var out struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", "", fmt.Errorf("ai: decode reply: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", out.Model, errors.New("ai: reply has no choices")
	}
	return out.Choices[0].Message.Content, out.Model, nil
}

// errMessage pulls a message out of either error shape (Outpost's
// Anthropic-style one, or a provider's OpenAI-style one), short enough to log.
func errMessage(raw []byte) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	msg := ""
	if json.Unmarshal(raw, &e) == nil {
		msg = e.Error.Message
	}
	if msg == "" {
		msg = strings.TrimSpace(string(raw))
	}
	if len(msg) > 200 {
		msg = msg[:200] + "…"
	}
	return msg
}
