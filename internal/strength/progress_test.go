package strength

import (
	"context"
	"fmt"
	"strings"
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

// putSession stores a finished session of Squat sets at 100 kg x 6, one per
// duration given (ms).
func putSession(t *testing.T, st *store.Store, id string, start int64, durs ...int64) {
	t.Helper()
	ctx := context.Background()
	fin := start + 400_000
	if err := st.PutSession(ctx, store.Session{ID: id, TemplateID: "t", Name: "A", Plan: []store.Exercise{{Name: "Squat"}},
		StartedAt: start, FinishedAt: &fin, UpdatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	for i, d := range durs {
		at := start + 100_000 + int64(i)*120_000
		if err := st.PutSet(ctx, store.Set{ID: fmt.Sprint(id, i), SessionID: id, ExerciseName: "Squat", SetIndex: i,
			TargetReps: 6, Reps: 6, WeightKg: 100, StartedAt: at, EndedAt: at + d, UpdatedAt: 1}); err != nil {
			t.Fatal(err)
		}
	}
}

func prescribeSquat(t *testing.T, st *store.Store) store.Exercise {
	t.Helper()
	four := []store.TargetSet{{Reps: 6, WeightKg: 100}, {Reps: 6, WeightKg: 100}, {Reps: 6, WeightKg: 100}, {Reps: 6, WeightKg: 100}}
	tpl := store.Template{ID: "t", Name: "A", Exercises: []store.Exercise{{Name: "Squat", Sets: four}}}
	out, err := Prescribe(context.Background(), st, tpl, Readiness{Level: "normal"})
	if err != nil {
		t.Fatal(err)
	}
	return out.Exercises[0]
}

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// A session ended after one set must not read as "every set hit" and add load.
func TestPrescribeIgnoresAbortedSession(t *testing.T) {
	st := openStore(t)
	putSession(t, st, "s1", 1000, 30_000)
	if ex := prescribeSquat(t, st); ex.Sets[0].WeightKg != 100 || ex.Change != "" {
		t.Fatalf("aborted session changed the prescription: %v kg, %q", ex.Sets[0].WeightKg, ex.Change)
	}
}

// A session tapped through in seconds says nothing about the lift.
func TestPrescribeIgnoresTappedSession(t *testing.T) {
	st := openStore(t)
	putSession(t, st, "s1", 1000, 1_000, 1_000, 1_000, 1_000)
	if ex := prescribeSquat(t, st); ex.Sets[0].WeightKg != 100 || ex.Change != "" {
		t.Fatalf("tapped session changed the prescription: %v kg, %q", ex.Sets[0].WeightKg, ex.Change)
	}
}

// One quick set among real ones is a slow tap on Finish: the session still
// hit every set, so the load goes up.
func TestPrescribeCountsAQuickSet(t *testing.T) {
	st := openStore(t)
	putSession(t, st, "s1", 1000, 30_000, 30_000, 2_000, 30_000)
	if ex := prescribeSquat(t, st); ex.Sets[0].WeightKg != 105 || !strings.HasPrefix(ex.Change, "Up 5 kg") {
		t.Fatalf("got %v kg, %q; want up 5 kg", ex.Sets[0].WeightKg, ex.Change)
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
	p := ProgressOf(plan, []store.Set{real, real, real})
	if p.Done != 3 || p.Short != 0 || p.Planned != 8 || p.Complete() {
		t.Fatalf("3 of 8 planned: got %+v complete=%v", p, p.Complete())
	}
	// Quick sets among real ones still count.
	p = ProgressOf(plan, []store.Set{real, real, real, tap})
	if p.Done != 4 || p.Short != 1 || !p.Complete() || p.Empty() {
		t.Fatalf("4 of 8 with one quick: got %+v complete=%v", p, p.Complete())
	}
	// Only taps: nothing was lifted.
	p = ProgressOf(plan, []store.Set{tap, tap, tap, tap})
	if !p.Empty() || p.Complete() {
		t.Fatalf("all taps: got %+v empty=%v complete=%v", p, p.Empty(), p.Complete())
	}
}

// A circuit where every exercise hit all its sets gets a round suggestion on
// its first exercise; weights still go up per exercise. One short exercise
// holds the suggestion back.
func TestPrescribeSuggestsCircuitRound(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	fin := int64(2_000_000)
	plan := []store.Exercise{{Name: "Row", Circuit: "a"}, {Name: "Press", Circuit: "a"}}
	if err := st.PutSession(ctx, store.Session{ID: "s1", TemplateID: "t", Name: "A", Plan: plan,
		StartedAt: 1000, FinishedAt: &fin, UpdatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	// Two rounds, alternating Row and Press; Press's last set fell short.
	at := int64(100_000)
	for round := 0; round < 2; round++ {
		for _, name := range []string{"Row", "Press"} {
			reps := 10
			if name == "Press" && round == 1 {
				reps = 8
			}
			if err := st.PutSet(ctx, store.Set{ID: fmt.Sprint(name, round), SessionID: "s1", ExerciseName: name, SetIndex: round,
				TargetReps: 10, Reps: reps, WeightKg: 40, StartedAt: at, EndedAt: at + 30_000, UpdatedAt: 1}); err != nil {
				t.Fatal(err)
			}
			at += 60_000
		}
	}
	sets := []store.TargetSet{{Reps: 10, WeightKg: 40}, {Reps: 10, WeightKg: 40}}
	tpl := store.Template{ID: "t", Name: "A", Exercises: []store.Exercise{
		{Name: "Row", Circuit: "a", Sets: sets}, {Name: "Press", Circuit: "a", Sets: sets}}}

	out, err := Prescribe(ctx, st, tpl, Readiness{Level: "normal"})
	if err != nil {
		t.Fatal(err)
	}
	if c := out.Exercises[0].Change; strings.Contains(c, "add a round") {
		t.Fatalf("suggested a round after a miss: %q", c)
	}

	// Fix the miss: now both hit, so Row goes up and the circuit gets the note.
	if err := st.PutSet(ctx, store.Set{ID: "Press1", SessionID: "s1", ExerciseName: "Press", SetIndex: 1,
		TargetReps: 10, Reps: 10, WeightKg: 40, StartedAt: 280_000, EndedAt: 310_000, UpdatedAt: 2}); err != nil {
		t.Fatal(err)
	}
	out, err = Prescribe(ctx, st, tpl, Readiness{Level: "normal"})
	if err != nil {
		t.Fatal(err)
	}
	if c := out.Exercises[0].Change; !strings.HasPrefix(c, "Up 2.5 kg") || !strings.Contains(c, "add a round") {
		t.Fatalf("row change %q", c)
	}
	if out.Exercises[0].Sets[0].WeightKg != 42.5 || strings.Contains(out.Exercises[1].Change, "add a round") {
		t.Fatalf("got %+v", out.Exercises)
	}
	// Recovery says hold: no round suggestion.
	out, _ = Prescribe(ctx, st, tpl, Readiness{Level: "hold"})
	if strings.Contains(out.Exercises[0].Change, "add a round") {
		t.Fatalf("suggested a round on a hold day: %q", out.Exercises[0].Change)
	}
}

// CircuitShare is the circuit's span over the session's working span; quick
// taps don't stretch either.
func TestCircuitShare(t *testing.T) {
	plan := []store.Exercise{{Name: "Squat"}, {Name: "Row", Circuit: "a"}, {Name: "Press", Circuit: "a"}}
	set := func(ex int, from, to int64) store.Set {
		return store.Set{ExerciseIndex: ex, Reps: 5, StartedAt: from * 1000, EndedAt: to * 1000}
	}
	sets := []store.Set{
		set(0, 0, 30), set(0, 150, 180), // squats: 0-180 s
		set(1, 300, 330), set(2, 340, 370), set(1, 460, 490), set(2, 500, 530), // circuit: 300-530 s
		set(0, 900, 901), // a tap, ignored
	}
	if got := CircuitShare(plan, sets); got != 0.43 { // 230 of 530 s
		t.Fatalf("share %v, want 0.43", got)
	}
	if got := CircuitShare(plan[:1], sets[:2]); got != 0 {
		t.Fatalf("no circuits: %v", got)
	}
}

func TestNextLoad(t *testing.T) {
	cases := []struct {
		ex, workout string
		last, want  float64
	}{
		{"Clean", "Kettle Bell Complex A", 16, 18},
		{"KB Swing", "Conditioning", 24, 26},
		{"Kettlebell Swing", "", 28, 32},
		{"Goblet Squat", "Kettlebell day", 48, 52},
		{"Curl", "Arms", 14, 16}, // a fixed dumbbell weight
		{"Curl", "Arms", 13, 14}, // odd whole kg: up to the next even
		{"Bench Press", "Upper A", 80, 82.5},
		{"Back Squat", "Lower A", 100, 105},
		{"Bench Press", "Upper A", 42.5, 45},
	}
	for _, c := range cases {
		if got := nextLoad(c.ex, c.workout, c.last); got != c.want {
			t.Errorf("nextLoad(%q, %q, %v) = %v, want %v", c.ex, c.workout, c.last, got, c.want)
		}
	}
}
