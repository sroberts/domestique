package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"domestique/internal/coach"
	"domestique/internal/store"
)

// Today shows the rule-based call while the LLM writes, then the LLM's
// wording, and the level (which drives the prescription) never changes.
func TestTodayReadinessFromLLM(t *testing.T) {
	a, st, h := testAPI(t)
	ctx := context.Background()
	if err := st.PutRecovery(ctx, store.Recovery{Date: "2026-11-03", Score: 20, Source: "test"}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "gemma4", "choices": []any{map[string]any{
			"message": map[string]any{"content": `{"title":"Easy day","detail":"Recovery is 20%. Spin in zone 2, or drop the last set and hold the weights."}`}}}})
	}))
	defer srv.Close()
	a.Coach = &coach.Writer{AI: &coach.AI{URL: srv.URL, Token: "oai_t", HTTP: srv.Client()}, KV: st, Log: a.Log}

	get := func() todayResp {
		w := call(t, h, "GET", "/v1/today?date=2026-11-03", "")
		if w.Code != 200 {
			t.Fatalf("today: %d %s", w.Code, w.Body)
		}
		var r todayResp
		_ = json.Unmarshal(w.Body.Bytes(), &r)
		return r
	}
	first := get()
	if first.Readiness.Title != "Ease off today" || !first.Readiness.Pending || first.Readiness.Source != "" {
		t.Fatalf("first: %+v, want the rule-based call, pending", first.Readiness)
	}
	var got todayResp
	for range 200 {
		if got = get(); got.Readiness.Source == "llm" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	r := got.Readiness
	if r.Source != "llm" || r.Title != "Easy day" || r.Level != "easy" || r.Pending {
		t.Fatalf("after writing: %+v", r)
	}
}
