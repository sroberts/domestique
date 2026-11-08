package coach

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type memKV struct {
	mu sync.Mutex
	m  map[string]string
}

func (k *memKV) SyncState(_ context.Context, key string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.m[key], nil
}
func (k *memKV) SetSyncState(_ context.Context, key, v string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.m == nil {
		k.m = map[string]string{}
	}
	k.m[key] = v
	return nil
}

func facts() Facts {
	rec, form := 72, 8
	return Facts{Date: "2026-11-07", Level: "push", Title: "Fresh and recovered",
		Detail:   "Recovery 72%, form +8. A good day for intensity or a heavier top set.",
		Recovery: &rec, Form: &form, Plan: "Lower A", PlanSets: 12}
}

// fakeAI answers like Outpost's /openai/v1/chat/completions with reply, or
// with status when it isn't 200.
func fakeAI(t *testing.T, status int, reply string, calls *atomic.Int32) *AI {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/openai/v1/chat/completions" || r.Method != http.MethodPost {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer oai_test" {
			t.Errorf("Authorization = %q", got)
		}
		var body struct {
			Model    string    `json:"model"`
			Messages []chatMsg `json:"messages"`
		}
		b, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(b, &body); err != nil || body.Model != "llm" || len(body.Messages) != 2 {
			t.Errorf("body = %s", b)
		}
		if status != http.StatusOK {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"api_error","message":"no LLM is configured in Outpost"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":   "gemma4:latest",
			"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": reply}}},
		})
	}))
	t.Cleanup(srv.Close)
	return &AI{URL: srv.URL, Token: "oai_test", HTTP: srv.Client()}
}

func waitIdle(t *testing.T, w *Writer) {
	t.Helper()
	for range 200 {
		w.mu.Lock()
		n := len(w.inflight)
		w.mu.Unlock()
		if n == 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("writer never finished")
}

func TestReadinessWrittenInBackground(t *testing.T) {
	var calls atomic.Int32
	reply := "<think>short and plain</think>\n" + `{"title": "Go hard today.", "detail": "Recovery 72% and form +8. Make the top set of Lower A heavier."}`
	w := &Writer{AI: fakeAI(t, 200, reply, &calls), KV: &memKV{}, Log: slog.New(slog.DiscardHandler)}
	ctx := context.Background()

	if _, ok, pending := w.Readiness(ctx, facts()); ok || !pending {
		t.Fatalf("first ask: ok=%v pending=%v, want a pending write", ok, pending)
	}
	waitIdle(t, w)
	c, ok, pending := w.Readiness(ctx, facts())
	if !ok || pending {
		t.Fatalf("second ask: ok=%v pending=%v", ok, pending)
	}
	if c.Title != "Go hard today" || !strings.HasPrefix(c.Detail, "Recovery 72%") || c.Model != "gemma4:latest" {
		t.Fatalf("call = %+v", c)
	}
	// Same facts: served from the store, no second request.
	w.Readiness(ctx, facts())
	if calls.Load() != 1 {
		t.Fatalf("model called %d times, want 1", calls.Load())
	}
	// New facts (a ride since): written again.
	f := facts()
	f.Recent = []Event{{When: "today", What: "Zwift", Min: 60, TSS: 70}}
	if _, ok, pending := w.Readiness(ctx, f); ok || !pending {
		t.Fatalf("changed facts: ok=%v pending=%v", ok, pending)
	}
	waitIdle(t, w)
	if calls.Load() != 2 {
		t.Fatalf("model called %d times, want 2", calls.Load())
	}
}

func TestReadinessUnavailableBacksOff(t *testing.T) {
	var calls atomic.Int32
	w := &Writer{AI: fakeAI(t, http.StatusServiceUnavailable, "", &calls), KV: &memKV{}, Log: slog.New(slog.DiscardHandler)}
	ctx := context.Background()
	w.Readiness(ctx, facts())
	waitIdle(t, w)
	_, ok, pending := w.Readiness(ctx, facts())
	if ok || pending {
		t.Fatalf("after a 503: ok=%v pending=%v, want the rule-based call alone", ok, pending)
	}
	if calls.Load() != 1 {
		t.Fatalf("model called %d times, want 1 (back off after failing)", calls.Load())
	}
}

func TestParseCallRejects(t *testing.T) {
	f := facts()
	for name, reply := range map[string]string{
		"not json":       "Fresh legs. Go hard.",
		"no detail":      `{"title": "Go hard"}`,
		"long title":     `{"title": "Today is a really very good day to go hard", "detail": "Recovery 72%."}`,
		"invented":       `{"title": "Go hard", "detail": "Recovery 72%. Push the squat to 140 kg."}`,
		"exclamation":    `{"title": "Go hard!", "detail": "Recovery 72%."}`,
		"long detail":    `{"title": "Go hard", "detail": "` + strings.Repeat("Recovery 72%. ", 25) + `"}`,
		"newline inside": `{"title": "Go\nhard", "detail": "Recovery 72%."}`,
	} {
		if c, err := parseCall(reply, f); err == nil {
			t.Errorf("%s: accepted %+v", name, c)
		}
	}
	if _, err := parseCall("```json\n"+`{"title": "Go hard", "detail": "Recovery 72%, form +8. Zone 2 tomorrow."}`+"\n```", f); err != nil {
		t.Errorf("fenced reply with known and small numbers refused: %v", err)
	}
}

func TestFromEnv(t *testing.T) {
	t.Setenv("OUTPOST_AI_URL", "")
	t.Setenv("OUTPOST_AI_TOKEN", "oai_x")
	if FromEnv() != nil {
		t.Fatal("AI on with no URL")
	}
	t.Setenv("OUTPOST_AI_URL", "http://127.0.0.1:47901/")
	a := FromEnv()
	if a == nil || a.URL != "http://127.0.0.1:47901" {
		t.Fatalf("FromEnv = %+v", a)
	}
}
