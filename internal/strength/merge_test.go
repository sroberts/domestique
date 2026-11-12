package strength

import (
	"testing"

	"domestique/internal/store"
)

func targets(n, reps int, kg float64) []store.TargetSet {
	out := make([]store.TargetSet, n)
	for i := range out {
		out[i] = store.TargetSet{Reps: reps, WeightKg: kg}
	}
	return out
}

// logged makes the sets a session did: per exercise index, how many.
func logged(session string, plan []store.Exercise, done map[int]int) []store.Set {
	var out []store.Set
	for i, n := range done {
		for k := range n {
			out = append(out, store.Set{ID: session + "-" + plan[i].Name + string(rune('0'+k)), SessionID: session,
				ExerciseIndex: i, ExerciseName: plan[i].Name, SetIndex: k,
				TargetReps: plan[i].Sets[k].Reps, TargetWeightKg: plan[i].Sets[k].WeightKg, Reps: plan[i].Sets[k].Reps})
		}
	}
	return out
}

func setIndexes(sets []store.Set, exercise int) []int {
	var out []int
	for _, s := range sets {
		if s.ExerciseIndex == exercise {
			out = append(out, s.SetIndex)
		}
	}
	return out
}

func TestMergePlanCircuitRoundsCarryOn(t *testing.T) {
	circuit := []store.Exercise{
		{Name: "Goblet squat", Sets: targets(3, 10, 20), RestSeconds: 30, Circuit: "a"},
		{Name: "Push-up", Sets: targets(3, 12, 0), RestSeconds: 90, Circuit: "a"},
	}
	a := store.Session{ID: "a", Plan: circuit}
	b := store.Session{ID: "b", Plan: circuit}
	// 1 round, then the same workout again for 3.
	plan, moved := MergePlan(a, logged("a", circuit, map[int]int{0: 1, 1: 1}), b, logged("b", circuit, map[int]int{0: 3, 1: 3}), nil)

	if len(plan) != 2 || len(plan[0].Sets) != 4 || len(plan[1].Sets) != 4 {
		t.Fatalf("plan %+v, want 2 exercises of 4 rounds", plan)
	}
	if plan[0].Circuit != "a" || plan[1].Circuit != "a" {
		t.Fatalf("circuit broken: %+v", plan)
	}
	for ex := range 2 {
		if got := setIndexes(moved, ex); len(got) != 3 || got[0] != 1 || got[2] != 3 {
			t.Errorf("exercise %d: b's sets are rounds %v, want 1-3 (0-based)", ex, got)
		}
	}
	for _, s := range moved {
		if s.SessionID != "a" {
			t.Fatalf("set %s still on %s", s.ID, s.SessionID)
		}
	}
	if p := ProgressOf(plan, append(logged("a", circuit, map[int]int{0: 1, 1: 1}), moved...)); p.Planned != 8 || p.Logged != 8 {
		t.Fatalf("progress %+v, want 8 of 8", p)
	}
}

func TestMergePlanStraightSets(t *testing.T) {
	plan := []store.Exercise{{Name: "Back squat", Sets: targets(4, 5, 100)}, {Name: "Romanian deadlift", Sets: targets(3, 8, 80)}}
	a := store.Session{ID: "a", Plan: plan}
	b := store.Session{ID: "b", Plan: plan}
	// a: 2 squat sets, then stopped. b: 3 squat sets, all the deadlifts.
	got, moved := MergePlan(a, logged("a", plan, map[int]int{0: 2}), b, logged("b", plan, map[int]int{0: 3, 1: 3}), nil)
	if len(got[0].Sets) != 5 || len(got[1].Sets) != 3 {
		t.Fatalf("plan sets %d and %d, want 5 squats (2 + 3) and 3 deadlifts", len(got[0].Sets), len(got[1].Sets))
	}
	if idx := setIndexes(moved, 0); idx[0] != 2 || idx[2] != 4 {
		t.Fatalf("b's squats are sets %v, want 2-4", idx)
	}
	if idx := setIndexes(moved, 1); idx[0] != 0 || idx[2] != 2 {
		t.Fatalf("deadlifts are sets %v, want 0-2: a never did any", idx)
	}
}

func TestMergePlanDifferentWorkouts(t *testing.T) {
	upper := []store.Exercise{
		{Name: "Bench press", Sets: targets(3, 8, 60)},
		{Name: "Row", Sets: targets(3, 10, 50), Circuit: "a"},
		{Name: "Curl", Sets: targets(3, 12, 12), Circuit: "a"},
	}
	core := []store.Exercise{
		{Name: "bench", Sets: targets(2, 8, 60)}, // an alias of Bench press
		{Name: "Plank", Sets: targets(3, 1, 0), Circuit: "a"},
		{Name: "Dead bug", Sets: targets(3, 10, 0), Circuit: "a"},
	}
	canon := store.Canon{"bench": "Bench press"}
	a := store.Session{ID: "a", Plan: upper}
	b := store.Session{ID: "b", Plan: core}
	got, moved := MergePlan(a, logged("a", upper, map[int]int{0: 3, 1: 3, 2: 3}), b, logged("b", core, map[int]int{0: 2, 1: 3, 2: 3}), canon)

	if len(got) != 5 {
		t.Fatalf("%d exercises, want 3 of a's and b's 2 new ones", len(got))
	}
	if len(got[0].Sets) != 5 || got[0].Name != "Bench press" {
		t.Fatalf("bench %+v, want a's name and 3 + 2 sets", got[0])
	}
	// b's circuit "a" must not join a's circuit "a".
	if got[3].Circuit == "a" || got[3].Circuit != got[4].Circuit || got[3].Name != "Plank" {
		t.Fatalf("b's circuit %+v / %+v should be its own", got[3], got[4])
	}
	if c := store.Circuits(got); len(c) != 2 {
		t.Fatalf("circuits %+v, want a's and b's apart", c)
	}
	if idx := setIndexes(moved, 3); len(idx) != 3 || idx[0] != 0 {
		t.Fatalf("plank sets %v, want 0-2", idx)
	}
	if a.Plan[0].Sets[0] != (store.TargetSet{Reps: 8, WeightKg: 60}) || len(a.Plan[0].Sets) != 3 {
		t.Fatal("a's own plan was changed")
	}
}

func TestMergePlanSetOutsideThePlan(t *testing.T) {
	plan := []store.Exercise{{Name: "Back squat", Sets: targets(2, 5, 100)}}
	a := store.Session{ID: "a", Plan: plan}
	b := store.Session{ID: "b", Plan: plan}
	stray := store.Set{ID: "x", SessionID: "b", ExerciseIndex: 7, ExerciseName: "Calf raise", SetIndex: 0, TargetReps: 15, TargetWeightKg: 40}
	got, moved := MergePlan(a, logged("a", plan, map[int]int{0: 2}), b, []store.Set{stray}, nil)
	if len(got) != 2 || got[1].Name != "Calf raise" || len(got[1].Sets) != 1 || moved[0].ExerciseIndex != 1 {
		t.Fatalf("plan %+v, moved %+v", got, moved)
	}
}
