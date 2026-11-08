package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"domestique/internal/coach"
	"domestique/internal/coach/coachtest"
	"domestique/internal/ingest"
	"domestique/internal/store"
	"domestique/internal/strength"
)

// Text becomes a draft, the draft becomes a finished session whose sets all
// count as lifting, and the session reaches activities.
func TestLogWorkoutFromText(t *testing.T) {
	a, st, h := testAPI(t)
	if w := call(t, h, "POST", "/v1/sessions/parse-log", `{"text":"3x10 push-ups"}`); w.Code != 503 {
		t.Fatalf("no model: %d %s", w.Code, w.Body)
	}
	yesterday := time.Now().In(a.loc()).AddDate(0, 0, -1).Format("2006-01-02")
	f := &coachtest.Fake{Chat: func(system, user string) string {
		if !strings.Contains(system, "ALREADY DONE") {
			t.Errorf("prompt: %q", system)
		}
		return fmt.Sprintf(`{"name":"","date":%q,"time":"07:30","minutes":0,
			"exercises":[{"name":"Push-Up","sets":[{"reps":10,"weight_kg":0},{"reps":10,"weight_kg":0},{"reps":8,"weight_kg":0}]},
			{"name":"Back Squat","sets":[{"reps":5,"weight_kg":80},{"reps":5,"weight_kg":80}]}],
			"not_logged":["20 min walk"]}`, yesterday)
	}}
	a.Coach = &coach.Writer{AI: f.AI(t), KV: a.Store, Log: a.Log}
	w := call(t, h, "POST", "/v1/sessions/parse-log", `{"text":"yesterday 7:30: 3x10 push-ups (last only 8), squats 2x5 at 80, 20 min walk"}`)
	var d loggedDraft
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &d) != nil {
		t.Fatalf("parse: %d %s", w.Code, w.Body)
	}
	start := time.UnixMilli(d.StartedAt).In(a.loc())
	if start.Format("2006-01-02 15:04") != yesterday+" 07:30" || d.Name != "Logged workout" || d.Minutes < 10 ||
		len(d.Exercises) != 2 || len(d.NotLogged) != 1 {
		t.Fatalf("draft = %+v (start %s)", d, start)
	}

	a.Ingest = &ingest.Ingester{Store: st, Log: a.Log, Loc: a.loc()}
	body, _ := json.Marshal(d)
	w = call(t, h, "POST", "/v1/sessions/logged", string(body))
	var saved struct{ ID string }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &saved) != nil {
		t.Fatalf("save: %d %s", w.Code, w.Body)
	}
	ctx := context.Background()
	sess, err := st.GetSession(ctx, saved.ID)
	if err != nil || sess.FinishedAt == nil || sess.StartedAt != d.StartedAt {
		t.Fatalf("session = %+v, %v", sess, err)
	}
	sets, _ := st.SessionSets(ctx, saved.ID)
	if len(sets) != 5 || len(strength.Counted(sets)) != 5 || sets[2].Reps != 8 || sets[4].WeightKg != 80 {
		t.Fatalf("sets = %+v", sets)
	}
	if *sess.FinishedAt < sets[4].EndedAt {
		t.Fatal("finished before its last set ended")
	}
	if _, err := st.ActivityBySession(ctx, saved.ID); err != nil {
		t.Fatalf("no activity: %v", err)
	}

	// Saving checks what it's given, model or no model.
	for name, bad := range map[string]string{
		"future":   fmt.Sprintf(`{"name":"X","started_at":%d,"minutes":30,"exercises":[{"name":"Squat","sets":[{"reps":5,"weight_kg":80}]}]}`, time.Now().Add(time.Hour).UnixMilli()),
		"no sets":  fmt.Sprintf(`{"name":"X","started_at":%d,"minutes":30,"exercises":[{"name":"Squat","sets":[]}]}`, d.StartedAt),
		"too long": fmt.Sprintf(`{"name":"X","started_at":%d,"minutes":900,"exercises":[{"name":"Squat","sets":[{"reps":5,"weight_kg":80}]}]}`, d.StartedAt),
	} {
		if w := call(t, h, "POST", "/v1/sessions/logged", bad); w.Code != 400 {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
}

func TestLoggedStart(t *testing.T) {
	now := time.Date(2026, 11, 8, 18, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		date, clock string
		want        string
	}{
		{"", "", "2026-11-08 17:00"},           // ended just now
		{"", "07:00", "2026-11-08 07:00"},      // this morning
		{"", "19:30", "2026-11-08 17:00"},      // later today: can't be
		{"2026-11-06", "", "2026-11-06 12:00"}, // a past day, no time
		{"2026-11-07", "06:15", "2026-11-07 06:15"},
	} {
		got := time.UnixMilli(loggedStart(now, c.date, c.clock, 60)).UTC().Format("2006-01-02 15:04")
		if got != c.want {
			t.Errorf("%q %q: %s, want %s", c.date, c.clock, got, c.want)
		}
	}
}

// An alias puts a new name's sets in the old name's history, PRs and
// exercise list, and undoing it takes them out again.
func TestExerciseAliases(t *testing.T) {
	a, st, h := testAPI(t)
	ctx := context.Background()
	day := time.Date(2026, 10, 1, 17, 0, 0, 0, time.UTC).UnixMilli()
	lift := func(id, name string, at int64, kg float64) {
		fin := at + 600_000
		plan := []store.Exercise{{Name: name, Sets: []store.TargetSet{{Reps: 5, WeightKg: kg}}}}
		if err := st.PutSession(ctx, store.Session{ID: id, TemplateID: "t", Name: "S", Plan: plan, StartedAt: at, FinishedAt: &fin, UpdatedAt: at}); err != nil {
			t.Fatal(err)
		}
		if err := st.PutSet(ctx, store.Set{ID: id + "_0", SessionID: id, ExerciseName: name, TargetReps: 5, TargetWeightKg: kg,
			Reps: 5, WeightKg: kg, StartedAt: at, EndedAt: at + 30_000, UpdatedAt: at}); err != nil {
			t.Fatal(err)
		}
	}
	lift("s1", "Bench Press", day, 60)
	lift("s2", "BB bench", day+86_400_000, 65)

	hist := func() int {
		hs, err := st.ExerciseHistory(ctx, "bench press", 6)
		if err != nil {
			t.Fatal(err)
		}
		return len(hs)
	}
	if hist() != 1 {
		t.Fatal("history before alias")
	}
	if err := st.PutAlias(ctx, store.Alias{Name: "BB bench", To: "Bench Press", P: 0.96, At: day}); err != nil {
		t.Fatal(err)
	}
	if hist() != 2 {
		t.Fatal("alias not in history")
	}
	if hs, _ := st.ExerciseHistory(ctx, "BB Bench", 6); len(hs) != 2 {
		t.Fatal("history asked by the alias's own name")
	}
	all, _ := st.AllSets(ctx)
	if all[1].ExerciseName != "Bench Press" {
		t.Fatalf("all sets: %+v", all)
	}
	var list []store.Alias
	if w := call(t, h, "GET", "/v1/exercise-aliases", ""); w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &list) != nil || len(list) != 1 {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	if w := call(t, h, "DELETE", "/v1/exercise-aliases/BB%20bench", ""); w.Code != 200 {
		t.Fatalf("undo: %d %s", w.Code, w.Body)
	}
	if hist() != 1 {
		t.Fatal("undone alias still in history")
	}
	if w := call(t, h, "DELETE", "/v1/exercise-aliases/BB%20bench", ""); w.Code != 404 {
		t.Fatalf("undo twice: %d", w.Code)
	}
	_ = a
}

func TestCanonChains(t *testing.T) {
	_, st, _ := testAPI(t)
	ctx := context.Background()
	for _, x := range []store.Alias{{Name: "bb bench", To: "Bench"}, {Name: "Bench", To: "Bench Press"}, {Name: "loop a", To: "loop b"}, {Name: "loop b", To: "loop a"}} {
		if err := st.PutAlias(ctx, x); err != nil {
			t.Fatal(err)
		}
	}
	c, err := st.Canon(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if c.Of("BB Bench") != "Bench Press" || c.Of("Squat") != "Squat" {
		t.Fatalf("canon = %v", c)
	}
	if n := c.Names("bench press"); len(n) != 3 {
		t.Fatalf("names = %v", n)
	}
	_ = c.Of("loop a") // terminates
}
