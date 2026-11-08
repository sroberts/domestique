package sweep_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"domestique/internal/coach"
	"domestique/internal/coach/coachtest"
	"domestique/internal/store"
)

func lifted(t *testing.T, st *store.Store, id, name string, at int64) {
	t.Helper()
	ctx := context.Background()
	fin := at + 600_000
	plan := []store.Exercise{{Name: name, Sets: []store.TargetSet{{Reps: 5, WeightKg: 60}}}}
	if err := st.PutSession(ctx, store.Session{ID: id, TemplateID: "t", Name: "S", Plan: plan, StartedAt: at, FinishedAt: &fin, UpdatedAt: at}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutSet(ctx, store.Set{ID: id + "_0", SessionID: id, ExerciseName: name, TargetReps: 5, TargetWeightKg: 60,
		Reps: 5, WeightKg: 60, StartedAt: at, EndedAt: at + 30_000, UpdatedAt: at}); err != nil {
		t.Fatal(err)
	}
}

// A new name System 1 is sure of becomes an alias of an older one; an
// unsure one, or a different lift, doesn't; nothing is asked twice, and an
// undone alias isn't asked about again.
func TestExerciseNames(t *testing.T) {
	f := &coachtest.Fake{SystemOne: func(state json.RawMessage, qs map[string]json.RawMessage) map[string]coach.Answer {
		var s struct {
			New   string   `json:"new_name"`
			Known []string `json:"exercises_the_athlete_logs"`
		}
		_ = json.Unmarshal(state, &s)
		if len(s.Known) == 0 || qs["same"] == nil {
			t.Errorf("question: %s %v", state, qs)
		}
		switch s.New {
		case "BB bench":
			return map[string]coach.Answer{"same": {Type: "choice", Choice: "Bench Press", Probabilities: map[string]float64{"Bench Press": 0.97}}}
		case "Incline Bench":
			return map[string]coach.Answer{"same": {Type: "choice", Choice: "Bench Press", Probabilities: map[string]float64{"Bench Press": 0.7}}}
		}
		return map[string]coach.Answer{"same": {Type: "choice", Choice: coach.NewExercise, Probabilities: map[string]float64{coach.NewExercise: 0.9}}}
	}}
	sw, st := setup(t, f)
	ctx := context.Background()
	day := time.Date(2026, 10, 1, 17, 0, 0, 0, time.UTC).UnixMilli()
	lifted(t, st, "s1", "Bench Press", day)
	lifted(t, st, "s2", "Back Squat", day+1000)
	lifted(t, st, "s3", "BB bench", day+86_400_000)
	lifted(t, st, "s4", "Incline Bench", day+2*86_400_000)

	sw.Run(ctx)
	as, _ := st.Aliases(ctx)
	if len(as) != 1 || as[0].Name != "BB bench" || as[0].To != "Bench Press" {
		t.Fatalf("aliases = %+v", as)
	}
	n := f.Calls.Load()
	if n != 3 {
		t.Fatalf("%d calls, want 3 (one per name after the first)", n)
	}
	sw.Run(ctx)
	if f.Calls.Load() != n {
		t.Fatal("asked again")
	}
	as[0].Undone = true
	_ = st.PutAlias(ctx, as[0])
	sw.Run(ctx)
	if c, _ := st.Canon(ctx); c.Of("BB bench") != "BB bench" || f.Calls.Load() != n {
		t.Fatalf("undone alias: canon %v, %d calls", c, f.Calls.Load()-n)
	}
	if !strings.EqualFold(as[0].Name, "bb bench") {
		t.Fatal("name")
	}
}
