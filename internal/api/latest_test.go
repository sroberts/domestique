package api

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"domestique/internal/store"
)

// A session where nothing counted (every set too short) stays out of Latest;
// one with real sets shows.
func TestLatestSkipsEmptySessions(t *testing.T) {
	_, st, h := testAPI(t)
	ctx := context.Background()
	now := time.Now().Add(-2 * time.Hour)
	plan := []store.Exercise{{Name: "Squat", Sets: []store.TargetSet{{Reps: 5, WeightKg: 100}, {Reps: 5, WeightKg: 100}}}}
	for i, id := range []string{"real", "empty"} {
		start := now.Add(time.Duration(i) * time.Hour).UnixMilli()
		fin := start + 20*60_000
		if err := st.PutSession(ctx, store.Session{ID: id, TemplateID: "t", Name: id, Plan: plan, StartedAt: start, FinishedAt: &fin, UpdatedAt: fin}); err != nil {
			t.Fatal(err)
		}
		hold := int64(30_000) // a real set
		if id == "empty" {
			hold = 1_000 // tapped through
		}
		for j := range 2 {
			at := start + int64(j)*120_000
			if err := st.PutSet(ctx, store.Set{ID: id + string(rune('a'+j)), SessionID: id, ExerciseName: "Squat", SetIndex: j,
				TargetReps: 5, TargetWeightKg: 100, Reps: 5, WeightKg: 100, StartedAt: at, EndedAt: at + hold, UpdatedAt: at}); err != nil {
				t.Fatal(err)
			}
		}
		putAct(t, st, "act-"+id, "strength", time.UnixMilli(start), 1200, id)
	}
	var got struct {
		Latest []ActivityRow `json:"latest"`
	}
	_ = json.Unmarshal(call(t, h, "GET", "/v1/today", "").Body.Bytes(), &got)
	if len(got.Latest) != 1 || got.Latest[0].ID != "act-real" {
		ids := []string{}
		for _, x := range got.Latest {
			ids = append(ids, x.ID)
		}
		t.Fatalf("latest = %v, want only act-real", ids)
	}
}
