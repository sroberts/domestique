package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"domestique/internal/store"
)

func testAPI(t *testing.T) (*API, *store.Store, http.Handler) {
	t.Helper()
	st, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	a := &API{Store: st, Log: slog.New(slog.DiscardHandler), Loc: time.UTC}
	mux := http.NewServeMux()
	a.Register(mux)
	return a, st, mux
}

func call(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(w, r)
	return w
}

const squat = `{"name":"Lower","exercises":[{"name":"Back squat","rest_seconds":180,
	"sets":[{"reps":5,"weight_kg":100},{"reps":5,"weight_kg":100},{"reps":5,"weight_kg":100}]}]}`

func TestPutTemplateValidates(t *testing.T) {
	_, _, h := testAPI(t)
	cases := []struct {
		name, body, want string
	}{
		{"no name", `{"name":"  ","exercises":[{"name":"Squat","sets":[{"reps":5,"weight_kg":100}]}]}`, "name is required"},
		{"no exercises", `{"name":"A","exercises":[]}`, "at least one exercise"},
		{"no sets", `{"name":"A","exercises":[{"name":"Squat","sets":[]}]}`, "at least one set"},
		{"unnamed exercise", `{"name":"A","exercises":[{"name":"","sets":[{"reps":5,"weight_kg":0}]}]}`, "exercise 1 needs a name"},
		{"zero reps", `{"name":"A","exercises":[{"name":"Squat","sets":[{"reps":0,"weight_kg":100}]}]}`, "reps must be 1-100"},
		{"too many reps", `{"name":"A","exercises":[{"name":"Squat","sets":[{"reps":101,"weight_kg":100}]}]}`, "reps must be 1-100"},
		{"heavy", `{"name":"A","exercises":[{"name":"Squat","sets":[{"reps":5,"weight_kg":1000.25}]}]}`, "0-1000"},
		{"negative", `{"name":"A","exercises":[{"name":"Squat","sets":[{"reps":5,"weight_kg":-2.5}]}]}`, "0-1000"},
		{"off step", `{"name":"A","exercises":[{"name":"Squat","sets":[{"reps":5,"weight_kg":100.1}]}]}`, "steps of 0.25"},
		{"long rest", `{"name":"A","exercises":[{"name":"Squat","rest_seconds":901,"sets":[{"reps":5,"weight_kg":100}]}]}`, "rest_seconds"},
	}
	for _, c := range cases {
		w := call(t, h, "PUT", "/v1/templates/t1", c.body)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), c.want) {
			t.Errorf("%s: %d %s, want 400 with %q", c.name, w.Code, w.Body.String(), c.want)
		}
	}
	if w := call(t, h, "PUT", "/v1/templates/bad%20id", squat); w.Code != http.StatusBadRequest {
		t.Errorf("bad id: %d", w.Code)
	}

	// Good: names trimmed, quarter-kilo steps and bodyweight accepted, and a
	// prescription note sent back by a client is not stored.
	body := `{"name":" Upper ","exercises":[{"name":" Curl ","rest_seconds":60,"change":"Up 2.5 kg",
		"sets":[{"reps":10,"weight_kg":12.25},{"reps":10,"weight_kg":0}]}]}`
	w := call(t, h, "PUT", "/v1/templates/t1", body)
	if w.Code != http.StatusOK {
		t.Fatalf("good template: %d %s", w.Code, w.Body.String())
	}
	var got store.Template
	_ = json.Unmarshal(call(t, h, "GET", "/v1/templates/t1", "").Body.Bytes(), &got)
	if got.Name != "Upper" || got.Exercises[0].Name != "Curl" || got.Exercises[0].Change != "" || got.Exercises[0].Sets[0].WeightKg != 12.25 {
		t.Fatalf("stored %+v", got)
	}
}

func TestDeleteTemplate(t *testing.T) {
	_, st, h := testAPI(t)
	ctx := context.Background()
	for _, id := range []string{"a", "b"} {
		if w := call(t, h, "PUT", "/v1/templates/"+id, squat); w.Code != http.StatusOK {
			t.Fatal(w.Body.String())
		}
	}
	// A past session of "a" must survive the template.
	fin := int64(2_000_000)
	plan := []store.Exercise{{Name: "Back squat", Sets: []store.TargetSet{{Reps: 5, WeightKg: 100}}}}
	if err := st.PutSession(ctx, store.Session{ID: "s1", TemplateID: "a", Name: "Lower", Plan: plan, StartedAt: 1_000_000, FinishedAt: &fin, UpdatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutPlan(ctx, "2026-11-01", "a"); err != nil { // past
		t.Fatal(err)
	}
	if err := st.PutPlan(ctx, "2026-11-03", "a"); err != nil { // today
		t.Fatal(err)
	}

	w := call(t, h, "DELETE", "/v1/templates/a?date=2026-11-03", "")
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "today's plan") {
		t.Fatalf("today's plan: %d %s, want 409", w.Code, w.Body.String())
	}
	if w := call(t, h, "DELETE", "/v1/templates/a?date=2026-11-02", ""); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "2026-11-03") {
		t.Fatalf("future plan: %d %s", w.Code, w.Body.String())
	}
	// Planned only in the past: deletes, and the past plan goes with it.
	if w := call(t, h, "DELETE", "/v1/templates/a?date=2026-11-04", ""); w.Code != http.StatusNoContent {
		t.Fatalf("past plan only: %d %s", w.Code, w.Body.String())
	}
	if _, err := st.GetPlan(ctx, "2026-11-03"); err != store.ErrNotFound {
		t.Fatalf("plan left behind: %v", err)
	}
	if s, err := st.GetSession(ctx, "s1"); err != nil || s.Plan[0].Name != "Back squat" {
		t.Fatalf("history lost: %v", err)
	}
	if w := call(t, h, "DELETE", "/v1/templates/a", ""); w.Code != http.StatusNotFound {
		t.Fatalf("again: %d", w.Code)
	}

	// clear_plans=1 clears today's plan and deletes.
	if err := st.PutPlan(ctx, "2026-11-03", "b"); err != nil {
		t.Fatal(err)
	}
	if w := call(t, h, "DELETE", "/v1/templates/b?date=2026-11-03&clear_plans=1", ""); w.Code != http.StatusNoContent {
		t.Fatalf("clear_plans: %d %s", w.Code, w.Body.String())
	}
	if _, err := st.GetPlan(ctx, "2026-11-03"); err != store.ErrNotFound {
		t.Fatalf("today's plan not cleared: %v", err)
	}
	if w := call(t, h, "GET", "/v1/plans/2026-11-03", ""); !strings.Contains(w.Body.String(), `"template_id":null`) {
		t.Fatalf("GET plan: %s", w.Body.String())
	}
}

func TestPrescribedTemplate(t *testing.T) {
	_, st, h := testAPI(t)
	ctx := context.Background()
	if w := call(t, h, "PUT", "/v1/templates/lower", squat); w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}
	// Last session hit every set at 100 kg.
	start := time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC).UnixMilli()
	fin := start + 900_000
	plan := []store.Exercise{{Name: "Back squat", Sets: make([]store.TargetSet, 3)}}
	if err := st.PutSession(ctx, store.Session{ID: "s1", TemplateID: "other", Name: "X", Plan: plan, StartedAt: start, FinishedAt: &fin, UpdatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{"x1", "x2", "x3"} {
		b := start + int64(i)*200_000
		if err := st.PutSet(ctx, store.Set{ID: id, SessionID: "s1", ExerciseName: "Back squat", SetIndex: i,
			TargetReps: 5, Reps: 5, WeightKg: 100, StartedAt: b, EndedAt: b + 30_000, UpdatedAt: 1}); err != nil {
			t.Fatal(err)
		}
	}
	type resp struct {
		Plan      store.Template         `json:"plan"`
		Template  store.Template         `json:"template"`
		Readiness struct{ Level string } `json:"readiness"`
	}
	get := func() resp {
		w := call(t, h, "GET", "/v1/templates/lower/prescribed?date=2026-11-03", "")
		if w.Code != http.StatusOK {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
		var r resp
		_ = json.Unmarshal(w.Body.Bytes(), &r)
		return r
	}
	r := get()
	if ex := r.Plan.Exercises[0]; ex.Sets[0].WeightKg != 105 || !strings.HasPrefix(ex.Change, "Up 5 kg") {
		t.Fatalf("not progressed: %+v", ex)
	}
	if r.Template.Exercises[0].Sets[0].WeightKg != 100 {
		t.Fatalf("raw template changed: %+v", r.Template)
	}

	// A red recovery holds the weight and drops a set, as Today would.
	if err := st.PutRecovery(ctx, store.Recovery{Date: "2026-11-03", Score: 20, Source: "test"}); err != nil {
		t.Fatal(err)
	}
	r = get()
	if ex := r.Plan.Exercises[0]; r.Readiness.Level != "easy" || ex.Sets[0].WeightKg != 100 || len(ex.Sets) != 2 {
		t.Fatalf("easy day: %s %+v", r.Readiness.Level, ex)
	}

	if w := call(t, h, "GET", "/v1/templates/nope/prescribed", ""); w.Code != http.StatusNotFound {
		t.Fatalf("missing: %d", w.Code)
	}
	if w := call(t, h, "GET", "/v1/templates/lower/prescribed?date=nov", ""); w.Code != http.StatusBadRequest {
		t.Fatalf("bad date: %d", w.Code)
	}

	// The list says when each template was last done, and suggestions carry
	// the last session's weight.
	var rows []struct {
		ID         string `json:"id"`
		LastDoneAt *int64 `json:"last_done_at"`
	}
	_ = json.Unmarshal(call(t, h, "GET", "/v1/templates", "").Body.Bytes(), &rows)
	if len(rows) != 1 || rows[0].LastDoneAt != nil {
		t.Fatalf("list: %+v", rows)
	}
	var ex []struct {
		Name string            `json:"name"`
		Sets []store.TargetSet `json:"sets"`
	}
	_ = json.Unmarshal(call(t, h, "GET", "/v1/exercises", "").Body.Bytes(), &ex)
	if len(ex) != 1 || ex[0].Name != "Back squat" || len(ex[0].Sets) != 3 || ex[0].Sets[0].WeightKg != 100 {
		t.Fatalf("exercises: %+v", ex)
	}
}
