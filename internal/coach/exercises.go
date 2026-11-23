package coach

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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
	ans, err := a.sameExercise(ctx, name, known)
	for err != nil && tooLong(err) && len(known) > 1 {
		// The names offered count against System 1's cap on a question:
		// offer the nearest half (callers order them best first) and ask again.
		known = known[:len(known)/2]
		ans, err = a.sameExercise(ctx, name, known)
	}
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

// exerciseQ is kept short: System 1 caps a question's instructions, and
// the options offered count against the cap. The new name is in the state.
const exerciseQ = "Is new_name one of these exercises written another way (abbreviation, plural, typo, word order)? " +
	"A variation is different: front squat is not back squat, incline bench is not flat bench."

func (a *AI) sameExercise(ctx context.Context, name string, known []string) (map[string]Answer, error) {
	criteria := map[string]string{NewExercise: "a different exercise, or a variation of one"}
	for _, k := range known {
		criteria[k] = "the same as " + k
	}
	ans, _, err := a.SystemOne(ctx, map[string]any{"new_name": name, "exercises_the_athlete_logs": known},
		map[string]Question{"same": {Type: "choice", Criteria: criteria, Instructions: exerciseQ}})
	return ans, err
}

// tooLong reports System 1 refusing a question as longer than its cap.
func tooLong(err error) bool {
	var se *StatusError
	return errors.As(err, &se) && se.Status == http.StatusBadRequest && strings.Contains(se.Message, "tokens; limit is")
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
