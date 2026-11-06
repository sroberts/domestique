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

func TestRealSet(t *testing.T) {
	set := func(reps int, secs float64) store.Set {
		return store.Set{Reps: reps, StartedAt: 1_000_000, EndedAt: 1_000_000 + int64(secs*1000)}
	}
	cases := []struct {
		reps int
		secs float64
		want bool
	}{
		{1, 4.9, false}, // a tap, even for a single
		{1, 5, true},
		{5, 5.5, false}, // five reps in five seconds wasn't lifted
		{5, 10, true},
		{10, 19, false},
		{10, 25, true},
	}
	for _, c := range cases {
		if got := Real(set(c.reps, c.secs)); got != c.want {
			t.Errorf("Real(%d reps, %.1fs) = %v, want %v", c.reps, c.secs, got, c.want)
		}
	}
}

func TestProgressComplete(t *testing.T) {
	plan := []store.Exercise{{Sets: make([]store.TargetSet, 4)}, {Sets: make([]store.TargetSet, 4)}}
	real := store.Set{Reps: 5, StartedAt: 0, EndedAt: 30_000}
	tap := store.Set{Reps: 5, StartedAt: 0, EndedAt: 1_000}
	p := ProgressOf(plan, []store.Set{real, real, real, tap, tap, tap})
	if p.Done != 3 || p.Planned != 8 || p.Complete() {
		t.Fatalf("3 real of 8 planned: got %+v complete=%v", p, p.Complete())
	}
	p = ProgressOf(plan, []store.Set{real, real, real, real})
	if !p.Complete() {
		t.Fatalf("4 of 8 should count as done: %+v", p)
	}
}
