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
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// AI calls Outpost's model endpoint. The zero value is unusable; build it
// with FromEnv.
type AI struct {
	URL   string // OUTPOST_AI_URL, no trailing slash
	Token string // OUTPOST_AI_TOKEN; never logged, sent only to URL
	HTTP  *http.Client

	once sync.Once
	sem  chan struct{}
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
	var ue *url.Error
	if errors.As(err, &ue) || errors.Is(err, context.DeadlineExceeded) {
		return true // network: the daemon is restarting or gone, or too slow
	}
	var se *StatusError
	if !errors.As(err, &se) {
		return false // a reply that couldn't be read or checked
	}
	switch se.Status {
	case http.StatusForbidden, http.StatusServiceUnavailable, http.StatusBadGateway, http.StatusTooManyRequests:
		return true
	}
	return false
}

// OneOff reports whether err concerns only the request that got it: a reply
// that couldn't be read or checked, or a request the provider refused as
// malformed. Anything else (no model, no access, an older Outpost without
// the endpoint, the network) would fail the next request the same way.
func OneOff(err error) bool {
	var ue *url.Error
	if errors.As(err, &ue) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return false
	}
	var se *StatusError
	if !errors.As(err, &se) {
		return true
	}
	switch se.Status {
	case http.StatusBadRequest, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity:
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
	raw, err := a.post(ctx, "/openai/v1/chat/completions", body)
	if err != nil {
		return "", "", err
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

// post sends one request to Outpost and returns the body of a 200. At most
// two run at once: Ollama queues the rest anyway, and a burst of page views
// shouldn't stack up minutes of work.
func (a *AI) post(ctx context.Context, path string, body []byte) ([]byte, error) {
	a.once.Do(func() { a.sem = make(chan struct{}, 2) })
	select {
	case a.sem <- struct{}{}:
		defer func() { <-a.sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.URL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.Token)
	resp, err := a.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ai: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("ai: read reply: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &StatusError{Status: resp.StatusCode, Message: errMessage(raw)}
	}
	return raw, nil
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

// Question is one System 1 question (service spec §15). Type is "noul" (yes
// or no), "choice" (Criteria maps label -> when it applies) or "score"
// (Levels, lowest first). Every provider takes instructions on every
// question, two or more labels and two to ten levels; keep to that.
type Question struct {
	Type         string            `json:"type"`
	Instructions string            `json:"instructions"`
	Criteria     map[string]string `json:"-"`
	Levels       []string          `json:"-"`
}

func (q Question) MarshalJSON() ([]byte, error) {
	m := map[string]any{"type": q.Type, "instructions": q.Instructions}
	switch q.Type {
	case "choice":
		m["criteria"] = q.Criteria
	case "score":
		m["criteria"] = q.Levels
	}
	return json.Marshal(m)
}

// Answer is System 1's answer to one question. Noul is the probability of
// yes; Choice the likeliest label; Score a position on the scale, levels
// numbered from 0. Type "refusal" means the model wouldn't say: treat it as
// "don't know".
type Answer struct {
	Type          string             `json:"type"`
	Noul          float64            `json:"noul"`
	Choice        string             `json:"choice"`
	Score         float64            `json:"score"`
	Confidence    float64            `json:"confidence"`
	Probabilities map[string]float64 `json:"probabilities"`
}

// SystemOne puts typed questions about state (a string, or a JSON-able
// value) to the operator's System 1 model in one pass.
func (a *AI) SystemOne(ctx context.Context, state any, qs map[string]Question) (map[string]Answer, string, error) {
	body, err := json.Marshal(map[string]any{"model": "system1", "state": state, "questions": qs})
	if err != nil {
		return nil, "", err
	}
	raw, err := a.post(ctx, "/systemone/v1/systemone", body)
	if err != nil {
		return nil, "", err
	}
	var out struct {
		Model   string            `json:"model"`
		Answers map[string]Answer `json:"answers"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, "", fmt.Errorf("ai: decode System 1 reply: %w", err)
	}
	return out.Answers, out.Model, nil
}
