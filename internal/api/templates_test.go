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

func TestPutTemplateCircuits(t *testing.T) {
	_, _, h := testAPI(t)
	set := `{"reps":10,"weight_kg":20}`
	ex := func(name, circuit string, sets int) string {
		s := strings.TrimSuffix(strings.Repeat(set+",", sets), ",")
		return `{"name":"` + name + `","circuit":"` + circuit + `","rest_seconds":0,"sets":[` + s + `]}`
	}
	tpl := func(exs ...string) string {
		return `{"name":"Circuit","exercises":[` + strings.Join(exs, ",") + `]}`
	}
	cases := []struct {
		name, body, want string
	}{
		{"alone", tpl(ex("Row", "a", 3), ex("Squat", "", 3)), "circuit a needs at least two exercises"},
		{"split", tpl(ex("Row", "a", 3), ex("Press", "a", 3), ex("Squat", "", 3), ex("Curl", "a", 3)), "next to each other"},
		{"uneven", tpl(ex("Row", "a", 3), ex("Press", "a", 2)), "same number of sets"},
		{"bad key", tpl(ex("Row", "a b", 3), ex("Press", "a b", 3)), "circuit must be"},
	}
	for _, c := range cases {
		w := call(t, h, "PUT", "/v1/templates/c1", c.body)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), c.want) {
			t.Errorf("%s: %d %s, want 400 with %q", c.name, w.Code, w.Body.String(), c.want)
		}
	}

	body := tpl(ex("Row", "a", 3), ex("Press", " a ", 3), ex("Squat", "", 5), ex("Curl", "b", 2), ex("Dip", "b", 2))
	if w := call(t, h, "PUT", "/v1/templates/c1", body); w.Code != http.StatusOK {
		t.Fatalf("good circuit template: %d %s", w.Code, w.Body.String())
	}
	var got store.Template
	_ = json.Unmarshal(call(t, h, "GET", "/v1/templates/c1", "").Body.Bytes(), &got)
	cs := store.Circuits(got.Exercises)
	if len(cs) != 2 || cs[0] != (store.Circuit{Key: "a", Start: 0, End: 2}) || cs[1] != (store.Circuit{Key: "b", Start: 3, End: 5}) {
		t.Fatalf("circuits %+v", cs)
	}
	if cs[0].Rounds(got.Exercises) != 3 {
		t.Fatalf("rounds %d", cs[0].Rounds(got.Exercises))
	}
}

// A circuit's rounds and rest between rounds can be sent as a circuit, not
// as per-exercise sets, and come back on every read.
func TestPutTemplateCircuitRounds(t *testing.T) {
	_, _, h := testAPI(t)
	body := `{"name":"Finisher","exercises":[
		{"name":"Swing","circuit":"a","rest_seconds":0,"sets":[{"reps":15,"weight_kg":24}]},
		{"name":"Push-up","circuit":"a","rest_seconds":15,"sets":[{"reps":12,"weight_kg":0}]},
		{"name":"Squat","rest_seconds":120,"sets":[{"reps":5,"weight_kg":100},{"reps":5,"weight_kg":100}]}],
		"circuits":[{"key":"a","rounds":4,"round_rest_seconds":90}]}`
	if w := call(t, h, "PUT", "/v1/templates/fin", body); w.Code != http.StatusOK {
		t.Fatalf("put: %d %s", w.Code, w.Body)
	}
	var got store.Template
	_ = json.Unmarshal(call(t, h, "GET", "/v1/templates/fin", "").Body.Bytes(), &got)
	if len(got.Exercises[0].Sets) != 4 || len(got.Exercises[1].Sets) != 4 || got.Exercises[1].Sets[3].Reps != 12 {
		t.Fatalf("sets %+v, want each set repeated for 4 rounds", got.Exercises)
	}
	if got.Exercises[1].RestSeconds != 90 || got.Exercises[0].RestSeconds != 0 || len(got.Exercises[2].Sets) != 2 {
		t.Fatalf("exercises %+v, want 90 s after the last of the circuit, the rest untouched", got.Exercises)
	}
	if len(got.Circuits) != 1 || got.Circuits[0].Key != "a" || got.Circuits[0].Rounds != 4 || *got.Circuits[0].RoundRestSeconds != 90 {
		t.Fatalf("circuits %+v", got.Circuits)
	}
	var list []store.Template
	_ = json.Unmarshal(call(t, h, "GET", "/v1/templates", "").Body.Bytes(), &list)
	if len(list) != 1 || len(list[0].Circuits) != 1 || list[0].Circuits[0].Rounds != 4 {
		t.Fatalf("list %+v, want circuits there too", list)
	}

	// Sending back what was read (sets already expanded) saves unchanged;
	// rounds alone leave the round rest as it was.
	again, _ := json.Marshal(got)
	if w := call(t, h, "PUT", "/v1/templates/fin", string(again)); w.Code != http.StatusOK {
		t.Fatalf("round trip: %d %s", w.Code, w.Body)
	}
	got.Circuits = []store.CircuitSpec{{Key: "a", Rounds: 4}}
	again, _ = json.Marshal(got)
	_ = json.Unmarshal(call(t, h, "PUT", "/v1/templates/fin", string(again)).Body.Bytes(), &got)
	if got.Exercises[1].RestSeconds != 90 {
		t.Fatalf("rest %d, want 90 kept", got.Exercises[1].RestSeconds)
	}

	for _, c := range []struct{ circuits, want string }{
		{`[{"key":"a","rounds":3}]`, "has 3 rounds, but Swing has 4 sets"},
		{`[{"key":"z","rounds":3}]`, "no exercise is in circuit z"},
		{`[{"key":"a","rounds":21}]`, "rounds must be 1-20"},
		{`[{"key":"a","round_rest_seconds":901}]`, "round_rest_seconds must be 0-900"},
		{`[{"key":"a"},{"key":"a"}]`, "listed twice"},
	} {
		got.Circuits = nil
		b, _ := json.Marshal(got)
		b = []byte(strings.Replace(string(b), `"updated_at"`, `"circuits":`+c.circuits+`,"updated_at"`, 1))
		if w := call(t, h, "PUT", "/v1/templates/fin", string(b)); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), c.want) {
			t.Errorf("%s: %d %s, want 400 with %q", c.circuits, w.Code, w.Body, c.want)
		}
	}
}
