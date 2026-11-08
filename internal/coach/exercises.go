package coach

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// ---- Exercise names ---------------------------------------------------------------

// NewExercise is the label for "none of these".
const NewExercise = "new exercise"

// MaxKnown is how many of the athlete's exercise names one question offers.
// Callers pick them with Nearest.
const MaxKnown = 6

// SameExercise asks System 1 whether an exercise name the athlete hasn't
// used before is one of the names they already log, written another way.
// It returns the matching known name and its probability, with no bar, or
// "" when System 1 leans to a different exercise or doesn't answer.
func (a *AI) SameExercise(ctx context.Context, name string, known []string) (string, float64, error) {
	if len(known) == 0 {
		return "", 0, nil
	}
	if len(known) > MaxKnown {
		return "", 0, fmt.Errorf("%d known names: at most %d", len(known), MaxKnown)
	}
	criteria := map[string]string{NewExercise: "a different exercise from all of these"}
	for _, k := range known {
		criteria[k] = fmt.Sprintf("the same exercise as %q, written another way", k)
	}
	ans, _, err := a.SystemOne(ctx, map[string]any{"new_name": name, "exercises_the_athlete_logs": known}, map[string]Question{
		"same": {Type: "choice", Criteria: criteria, Instructions: fmt.Sprintf(
			"The athlete just logged an exercise called %q. Is it one of the exercises they already log, written another way "+
				"(an abbreviation, a plural, a typo, a different word order, the equipment named differently), or a different exercise? "+
				"A variation that trains differently is a different exercise: front squat is not back squat, incline bench is not flat bench, "+
				"dumbbell press is not barbell press, Romanian deadlift is not deadlift.", name)},
	})
	if err != nil {
		return "", 0, err
	}
	s := ans["same"]
	if s.Type != "choice" || s.Choice == NewExercise {
		return "", 0, nil
	}
	for _, k := range known {
		if k == s.Choice {
			return k, s.Probabilities[k], nil
		}
	}
	return "", 0, fmt.Errorf("system 1 answered an exercise it wasn't offered: %q", s.Choice)
}

// Nearest picks the known names most like name, at most n, best first, by
// shared words, shared letter pairs, and initials ("RDL" for "Romanian
// Deadlift").
func Nearest(name string, known []string, n int) []string {
	type scored struct {
		k string
		s float64
	}
	var all []scored
	for _, k := range known {
		all = append(all, scored{k, likeness(name, k)})
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].s > all[j].s })
	out := []string{}
	for _, x := range all {
		if len(out) == n {
			break
		}
		out = append(out, x.k)
	}
	return out
}

func words(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

func pairs(s string) map[string]bool {
	m := map[string]bool{}
	r := []rune(strings.Join(words(s), " "))
	for i := 0; i+1 < len(r); i++ {
		m[string(r[i:i+2])] = true
	}
	return m
}

func likeness(a, b string) float64 {
	pa, pb := pairs(a), pairs(b)
	both := 0
	for p := range pa {
		if pb[p] {
			both++
		}
	}
	score := 0.0
	if len(pa)+len(pb) > 0 {
		score = 2 * float64(both) / float64(len(pa)+len(pb))
	}
	wa, wb := words(a), words(b)
	for _, w := range wa {
		if abbreviates(w, b) {
			score += 0.5
		}
	}
	for _, w := range wb {
		if abbreviates(w, a) {
			score += 0.5
		}
	}
	return score
}

// abbreviates reports whether a short word reads as an abbreviation of
// name: its letters in order in name's, starting with name's first ("RDL"
// for "Romanian Deadlift", "OHP" for "Overhead Press").
func abbreviates(w, name string) bool {
	r, n := []rune(w), []rune(strings.Join(words(name), ""))
	if len(r) < 2 || len(r) > 4 || len(n) <= len(r) || r[0] != n[0] {
		return false
	}
	i := 0
	for _, c := range n {
		if i < len(r) && c == r[i] {
			i++
		}
	}
	return i == len(r)
}
