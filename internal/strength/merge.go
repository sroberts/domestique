package strength

import (
	"strconv"
	"strings"

	"domestique/internal/store"
)

// MergePlan joins session b onto session a, where a started first: one
// workout that was saved as two. Exercises with the same name (after the
// athlete's aliases) carry on where a left off, so b's sets for them follow
// a's: a circuit done for 1 round and then 3 more reads as 4 rounds. For
// those exercises the plan becomes what the two did between them (unless
// neither did any, when a's plan stands). b's other exercises are added
// after a's, with b's plan for them.
//
// It returns the joined plan and b's sets moved onto it: session, exercise
// and set numbers change, nothing else.
func MergePlan(a store.Session, aSets []store.Set, b store.Session, bSets []store.Set, canon store.Canon) ([]store.Exercise, []store.Set) {
	key := func(n string) string { return strings.ToLower(strings.TrimSpace(canon.Of(n))) }

	plan := make([]store.Exercise, len(a.Plan))
	keys := map[string]bool{}
	for i, ex := range a.Plan {
		ex.Sets = append([]store.TargetSet(nil), ex.Sets...)
		plan[i] = ex
		if ex.Circuit != "" {
			keys[ex.Circuit] = true
		}
	}
	// How far a got in each exercise: the sets after that are b's.
	done, doneB := map[int]int{}, map[int]int{}
	for _, s := range aSets {
		done[s.ExerciseIndex] = max(done[s.ExerciseIndex], s.SetIndex+1)
	}
	for _, s := range bSets {
		doneB[s.ExerciseIndex] = max(doneB[s.ExerciseIndex], s.SetIndex+1)
	}

	to, offset := map[int]int{}, map[int]int{}
	used := map[int]bool{}
	renamed := map[string]string{}
	for j, ex := range b.Plan {
		i := -1
		for k := range a.Plan {
			if !used[k] && key(a.Plan[k].Name) == key(ex.Name) {
				i = k
				break
			}
		}
		if i >= 0 {
			used[i] = true
			to[j], offset[j] = i, done[i]
			if done[i]+doneB[j] == 0 {
				continue
			}
			sets := append([]store.TargetSet(nil), plan[i].Sets[:min(done[i], len(plan[i].Sets))]...)
			for len(sets) < done[i] { // a did more than it planned
				sets = append(sets, store.TargetSet{})
			}
			plan[i].Sets = append(sets, ex.Sets[:min(doneB[j], len(ex.Sets))]...)
			continue
		}
		// A circuit of b's that shares a key with one of a's would join it.
		if c := ex.Circuit; c != "" {
			if _, ok := renamed[c]; !ok {
				n := c
				for k := 2; keys[n]; k++ {
					n = c + "-" + strconv.Itoa(k)
				}
				renamed[c], keys[n] = n, true
			}
			ex.Circuit = renamed[c]
		}
		ex.Sets = append([]store.TargetSet(nil), ex.Sets...)
		to[j] = len(plan)
		plan = append(plan, ex)
	}

	moved := make([]store.Set, 0, len(bSets))
	for _, s := range bSets {
		j := s.ExerciseIndex
		i, ok := to[j]
		if !ok {
			// A set outside b's plan (an older or hand-made session): find
			// its exercise by name, or give it one.
			i = -1
			for k := range plan {
				if key(plan[k].Name) == key(s.ExerciseName) {
					i = k
					break
				}
			}
			if i < 0 {
				i = len(plan)
				plan = append(plan, store.Exercise{Name: s.ExerciseName})
			}
			to[j], offset[j] = i, len(plan[i].Sets)
		}
		s.SessionID = a.ID
		s.ExerciseIndex = i
		s.SetIndex += offset[j]
		// Every set needs a planned target to sit on.
		for len(plan[i].Sets) <= s.SetIndex {
			plan[i].Sets = append(plan[i].Sets, store.TargetSet{Reps: s.TargetReps, WeightKg: s.TargetWeightKg})
		}
		moved = append(moved, s)
	}
	return plan, moved
}
