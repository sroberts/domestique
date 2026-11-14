package mcp

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"domestique/internal/api"
	"domestique/internal/store"
)

func TestBatchIsBounded(t *testing.T) {
	calls := 0
	s := &Server{API: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})}
	one := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_trends"}}`
	post := func(n int) *httptest.ResponseRecorder {
		body := "[" + strings.TrimSuffix(strings.Repeat(one+",", n), ",") + "]"
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("POST", "/mcp", strings.NewReader(body)))
		return w
	}
	if w := post(maxBatch); calls != maxBatch || !strings.Contains(w.Body.String(), `"result"`) {
		t.Fatalf("batch of %d: %d calls, %s", maxBatch, calls, w.Body.String())
	}
	calls = 0
	if w := post(maxBatch + 1); calls != 0 || !strings.Contains(w.Body.String(), "batch larger") {
		t.Fatalf("oversized batch ran %d calls: %s", calls, w.Body.String())
	}
}

// save_template, list_templates and delete_template run the REST handlers,
// so Claude gets the same validation and the same today's-plan guard.
func TestTemplateTools(t *testing.T) {
	st, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := &api.API{Store: st, Log: slog.New(slog.DiscardHandler), Loc: time.Local}
	mux := http.NewServeMux()
	a.Register(mux)
	s := &Server{API: mux}
	callTool := func(name, args string) string {
		body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + name + `","arguments":` + args + `}}`
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("POST", "/mcp", strings.NewReader(body)))
		return w.Body.String()
	}
	bad := `{"id":"push","name":"Push","exercises":[{"name":"Bench","rest_seconds":120,"sets":[{"reps":5,"weight_kg":80.3}]}]}`
	if out := callTool("save_template", bad); !strings.Contains(out, `"isError":true`) || !strings.Contains(out, "steps of 0.25") {
		t.Fatalf("bad weight accepted: %s", out)
	}
	good := strings.Replace(bad, "80.3", "80.25", 1)
	if out := callTool("save_template", good); strings.Contains(out, "isError") {
		t.Fatalf("save: %s", out)
	}
	if out := callTool("list_templates", `{}`); !strings.Contains(out, `\"id\":\"push\"`) {
		t.Fatalf("list: %s", out)
	}
	if err := st.PutPlan(context.Background(), time.Now().Format("2006-01-02"), "push"); err != nil {
		t.Fatal(err)
	}
	if out := callTool("delete_template", `{"id":"push"}`); !strings.Contains(out, "today's plan") {
		t.Fatalf("deleted today's plan without asking: %s", out)
	}
	if out := callTool("delete_template", `{"id":"push","clear_plans":true}`); !strings.Contains(out, `"deleted":"push"`) {
		t.Fatalf("delete: %s", out)
	}
	if n, _ := st.CountTemplates(context.Background()); n != 0 {
		t.Fatalf("%d templates left", n)
	}
}

// merge_strength_sessions runs the REST merge, refusals included.
func TestMergeSessionsTool(t *testing.T) {
	st, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	a := &api.API{Store: st, Log: slog.New(slog.DiscardHandler), Loc: time.UTC}
	mux := http.NewServeMux()
	a.Register(mux)
	s := &Server{API: mux}
	callTool := func(name, args string) string {
		body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + name + `","arguments":` + args + `}}`
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("POST", "/mcp", strings.NewReader(body)))
		return w.Body.String()
	}
	plan := []store.Exercise{{Name: "Squat", Sets: []store.TargetSet{{Reps: 5, WeightKg: 100}, {Reps: 5, WeightKg: 100}}}}
	day := time.Date(2026, 11, 3, 7, 0, 0, 0, time.UTC).UnixMilli()
	for i, id := range []string{"one", "two"} {
		start := day + int64(i)*3_600_000
		fin := start + 600_000
		if err := st.PutSession(ctx, store.Session{ID: id, TemplateID: "lower", Name: "Lower", Plan: plan, StartedAt: start, FinishedAt: &fin, UpdatedAt: fin}); err != nil {
			t.Fatal(err)
		}
		if err := st.PutSet(ctx, store.Set{ID: id + "-s", SessionID: id, ExerciseName: "Squat", Reps: 5, WeightKg: 100, StartedAt: start, EndedAt: start + 30_000, UpdatedAt: fin}); err != nil {
			t.Fatal(err)
		}
	}
	if out := callTool("merge_strength_sessions", `{"first":"two","second":"two"}`); !strings.Contains(out, `"isError":true`) {
		t.Fatalf("merged a session with itself: %s", out)
	}
	if out := callTool("merge_strength_sessions", `{"first":"two","second":"one"}`); !strings.Contains(out, `\"id\":\"one\"`) {
		t.Fatalf("merge: %s", out)
	}
	if sets, _ := st.SessionSets(ctx, "one"); len(sets) != 2 {
		t.Fatalf("%d sets on the kept session, want 2", len(sets))
	}
}

func TestRidePlanTools(t *testing.T) {
	st, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := &api.API{Store: st, Log: slog.New(slog.DiscardHandler), Loc: time.UTC}
	mux := http.NewServeMux()
	a.Register(mux)
	s := &Server{API: mux}
	callTool := func(name, args string) string {
		body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + name + `","arguments":` + args + `}}`
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("POST", "/mcp", strings.NewReader(body)))
		return w.Body.String()
	}
	if out := callTool("plan_ride", `{"date":"2026-11-20","name":"Zwift: Jungle Circuit","kind":"indoor","duration_min":75,"intensity":"tempo"}`); strings.Contains(out, `"isError":true`) {
		t.Fatalf("plan_ride: %s", out)
	}
	if out := callTool("plan_ride", `{"date":"2026-11-21","name":"Ride","kind":"gravel","duration_min":75}`); !strings.Contains(out, `"isError":true`) {
		t.Fatalf("planned a gravel kind: %s", out)
	}
	if out := callTool("get_planned_rides", `{"from":"2026-11-19"}`); !strings.Contains(out, "Jungle Circuit") {
		t.Fatalf("get_planned_rides: %s", out)
	}
	if out := callTool("clear_planned_ride", `{"date":"2026-11-20"}`); strings.Contains(out, `"isError":true`) {
		t.Fatalf("clear_planned_ride: %s", out)
	}
	if _, err := st.GetRidePlan(context.Background(), "2026-11-20"); err == nil {
		t.Fatal("still planned after clearing")
	}
}
