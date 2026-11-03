package strength

import (
	"context"
	"fmt"
	"testing"

	"domestique/internal/store"
)

// Bodyweight lifts progress by reps from the last session's target: after a
// session prescribed at 7 reps that hit every set, the next is 8, not the
// template's 6+1 again.
func TestPrescribeBodyweightBuildsOnLastTarget(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	fin := int64(400_000)
	if err := st.PutSession(ctx, store.Session{ID: "s1", TemplateID: "t", Name: "A", Plan: []store.Exercise{{Name: "Pull-up"}},
		StartedAt: 1000, FinishedAt: &fin, UpdatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{"a", "b"} {
		if err := st.PutSet(ctx, store.Set{ID: id, SessionID: "s1", ExerciseName: "Pull-up", SetIndex: i,
			TargetReps: 7, Reps: 7, StartedAt: int64(100_000 + i*120_000), EndedAt: int64(130_000 + i*120_000), UpdatedAt: 1}); err != nil {
			t.Fatal(err)
		}
	}
	tpl := store.Template{ID: "t", Name: "A", Exercises: []store.Exercise{{Name: "Pull-up", Sets: []store.TargetSet{{Reps: 6}, {Reps: 6}}}}}
	out, err := Prescribe(ctx, st, tpl, Readiness{Level: "normal"})
	if err != nil {
		t.Fatal(err)
	}
	if got := out.Exercises[0].Sets[0].Reps; got != 8 {
		t.Fatalf("reps = %d, want 8 (%s)", got, out.Exercises[0].Change)
	}
}

// A session ended after one set (or sets tapped through in seconds) must not
// read as "every set hit" and add load.
func TestPrescribeIgnoresAbortedSession(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	fin := int64(400_000)
	if err := st.PutSession(ctx, store.Session{ID: "s1", TemplateID: "t", Name: "A", Plan: []store.Exercise{{Name: "Squat"}},
		StartedAt: 1000, FinishedAt: &fin, UpdatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	// One real set of four, plus one tapped through in a second.
	for i, d := range []int64{30_000, 1_000} {
		if err := st.PutSet(ctx, store.Set{ID: fmt.Sprint("x", i), SessionID: "s1", ExerciseName: "Squat", SetIndex: i,
			TargetReps: 6, Reps: 6, WeightKg: 100, StartedAt: int64(100_000 + i*120_000), EndedAt: int64(100_000+i*120_000) + d, UpdatedAt: 1}); err != nil {
			t.Fatal(err)
		}
	}
	four := []store.TargetSet{{Reps: 6, WeightKg: 100}, {Reps: 6, WeightKg: 100}, {Reps: 6, WeightKg: 100}, {Reps: 6, WeightKg: 100}}
	tpl := store.Template{ID: "t", Name: "A", Exercises: []store.Exercise{{Name: "Squat", Sets: four}}}
	out, err := Prescribe(ctx, st, tpl, Readiness{Level: "normal"})
	if err != nil {
		t.Fatal(err)
	}
	if got := out.Exercises[0].Sets[0].WeightKg; got != 100 || out.Exercises[0].Change != "" {
		t.Fatalf("aborted session changed the prescription: %v kg, %q", got, out.Exercises[0].Change)
	}
}
