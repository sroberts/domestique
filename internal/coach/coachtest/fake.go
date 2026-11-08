// Package coachtest is a stand-in for Outpost's AI endpoint (service spec
// §15) for tests: chat completions and System 1, answered by functions.
package coachtest

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"domestique/internal/coach"
)

// Fake answers like Outpost. Chat gets the system and user messages and
// returns the reply text; SystemOne gets the state and question IDs and
// returns answers. A nil function answers 503, as Outpost does for a kind
// with no model chosen.
type Fake struct {
	Chat      func(system, user string) string
	SystemOne func(state json.RawMessage, questions map[string]json.RawMessage) map[string]coach.Answer
	Calls     atomic.Int32
}

// AI starts the fake and returns a client for it.
func (f *Fake) AI(t *testing.T) *coach.AI {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.Calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer oai_test" {
			http.Error(w, `{"type":"error","error":{"message":"bad token"}}`, http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		unavailable := func() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"api_error","message":"no model is configured in Outpost"}}`))
		}
		switch r.URL.Path {
		case "/openai/v1/chat/completions":
			var in struct {
				Model    string `json:"model"`
				Messages []struct {
					Content string `json:"content"`
				} `json:"messages"`
			}
			if json.Unmarshal(body, &in) != nil || in.Model != "llm" || len(in.Messages) != 2 {
				t.Errorf("chat body: %s", body)
			}
			if f.Chat == nil {
				unavailable()
				return
			}
			reply := f.Chat(in.Messages[0].Content, in.Messages[1].Content)
			_ = json.NewEncoder(w).Encode(map[string]any{"model": "fake-llm",
				"choices": []any{map[string]any{"message": map[string]any{"content": reply}}}})
		case "/systemone/v1/systemone":
			var in struct {
				Model     string                     `json:"model"`
				State     json.RawMessage            `json:"state"`
				Questions map[string]json.RawMessage `json:"questions"`
			}
			if json.Unmarshal(body, &in) != nil || in.Model != "system1" || len(in.Questions) == 0 {
				t.Errorf("system 1 body: %s", body)
			}
			if f.SystemOne == nil {
				unavailable()
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"model": "fake-s1", "answers": f.SystemOne(in.State, in.Questions)})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return &coach.AI{URL: srv.URL, Token: "oai_test", HTTP: srv.Client()}
}
