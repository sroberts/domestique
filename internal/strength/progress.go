package strength

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"domestique/internal/store"
)

// Readiness summarizes how hard today should be.
type Readiness struct {
	Level  string `json:"level"` // push | normal | hold | easy
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

// AssessReadiness combines the morning recovery score with training form.
// rec may be nil; tsb is today's form (fitness minus fatigue).
func AssessReadiness(rec *store.Recovery, tsb float64, hasLoad bool) Readiness {
	score := -1
	if rec != nil {
		score = rec.Score
	}
	switch {
	case score >= 0 && score < 34:
		return Readiness{"easy", "Ease off today", fmt.Sprintf("Recovery is %d%%. Drop the last set of each lift and hold the weights, or swap the session for an easy spin in zone 1–2.", score)}
	case hasLoad && tsb < -30:
		return Readiness{"easy", "Carrying a lot of fatigue", fmt.Sprintf("Form is %.0f. Keep today easy: hold the weights, no intervals.", tsb)}
	case (score >= 34 && score < 67) || (hasLoad && tsb < -20):
		detail := "Train as planned, but hold the weights rather than adding load."
		if score >= 0 {
			detail = fmt.Sprintf("Recovery is %d%%. ", score) + detail
		}
		return Readiness{"hold", "Train, but hold the weights", detail}
	case score >= 67 && hasLoad && tsb > 5:
		return Readiness{"push", "Fresh and recovered", fmt.Sprintf("Recovery %d%%, form %+.0f. A good day for intensity or a heavier top set.", score, tsb)}
	case score >= 67 && hasLoad:
		return Readiness{"normal", "Train as planned", fmt.Sprintf("Recovery %d%% and form %+.0f: nothing in the way of today's plan.", score, tsb)}
	case score >= 67:
		return Readiness{"normal", "Train as planned", fmt.Sprintf("Recovery is %d%%.", score)}
	case hasLoad:
		return Readiness{"normal", "Train as planned", fmt.Sprintf("No recovery score yet; form is %+.0f.", tsb)}
	}
	return Readiness{"normal", "Train as planned", "No recovery score yet."}
}

// increment is the load step for an exercise: bigger for lower-body compounds.
func increment(name string) float64 {
	l := strings.ToLower(name)
	for _, k := range []string{"squat", "deadlift", "leg press", "hip thrust"} {
		if strings.Contains(l, k) && !strings.Contains(l, "split") && !strings.Contains(l, "romanian") {
			return 5
		}
	}
	return 2.5
}

// Prescribe turns a template into today's targets from each lift's history:
// add load after a session where every set hit its reps, hold after a miss,
// back off 10% after two misses in a row. Readiness "hold" blocks increases;
// "easy" also drops the last set of each lift.
func Prescribe(ctx context.Context, st *store.Store, t store.Template, r Readiness) (store.Template, error) {
	out := t
	out.Exercises = make([]store.Exercise, len(t.Exercises))
	// hitWhen marks exercises whose last session hit every planned set, with
	// that session's day, for the circuit round suggestion below.
	hitWhen := make([]string, len(t.Exercises))
	for i, ex := range t.Exercises {
		ex.Sets = append([]store.TargetSet(nil), ex.Sets...)
		raw, err := st.ExerciseHistory(ctx, ex.Name, 6)
		if err != nil {
			return t, err
		}
		// Only sessions that really trained this lift count: a session ended
		// after one set, or sets tapped through in seconds, say nothing about
		// whether the weight should go up.
		var hist [][]store.Set
		for _, h := range raw {
			if c := Counted(h); len(c)*2 >= len(ex.Sets) && len(c) > 0 {
				hist = append(hist, c)
			}
		}
		change := ""
		if len(hist) > 0 && len(hist[0]) > 0 {
			last := hist[0]
			lastW := 0.0
			for _, s := range last {
				lastW = math.Max(lastW, s.WeightKg)
			}
			when := time.UnixMilli(last[0].StartedAt).Format("Mon 2 Jan")
			hit := hitAll(last) && len(last) >= len(ex.Sets)
			short := hitAll(last) && len(last) < len(ex.Sets)
			missedTwice := !hit && !short && len(hist) > 1 && !hitAll(hist[1])
			if hit {
				hitWhen[i] = when
			}
			bodyweight := lastW == 0
			// Bodyweight progression adds reps, so build on last session's
			// target, not the template's, or it never gets past template+1.
			targetReps := ex.Sets[0].Reps
			if bodyweight {
				for _, s := range last {
					targetReps = max(targetReps, s.TargetReps)
				}
			}
			w := lastW
			switch {
			case missedTwice && !bodyweight:
				w = roundTo(lastW*0.9, 2.5)
				change = fmt.Sprintf("Back off to %s kg: missed reps two sessions running", fmtKg(w))
			case short:
				change = fmt.Sprintf("Hold %s: %d of %d sets done on %s", fmtLoad(lastW), len(last), len(ex.Sets), when)
			case !hit:
				change = fmt.Sprintf("Hold %s: some sets fell short on %s", fmtLoad(lastW), when)
			case r.Level == "hold" || r.Level == "easy":
				change = fmt.Sprintf("Hold %s: every set hit last time, but recovery says wait", fmtLoad(lastW))
			case bodyweight:
				if targetReps < 8 {
					targetReps++
					change = fmt.Sprintf("Up to %d reps: every set hit on %s", targetReps, when)
				} else {
					change = "Every set hit 8: add a 2.5 kg plate"
					w = 2.5
				}
			default:
				w = lastW + increment(ex.Name)
				change = fmt.Sprintf("Up %s kg: every set hit on %s", fmtKg(increment(ex.Name)), when)
			}
			for j := range ex.Sets {
				ex.Sets[j].WeightKg = w
				if bodyweight && w == 0 {
					ex.Sets[j].Reps = targetReps
				}
			}
		}
		if r.Level == "easy" && len(ex.Sets) > 1 {
			ex.Sets = ex.Sets[:len(ex.Sets)-1]
			if change != "" {
				change += ". "
			}
			change += "One set fewer today"
		}
		ex.Change = change
		out.Exercises[i] = ex
	}
	if r.Level != "hold" && r.Level != "easy" {
		suggestRounds(out.Exercises, hitWhen)
	}
	return out, nil
}

// suggestRounds notes on the first exercise of a circuit that a round could
// be added when every exercise in it hit all its sets last time. It only
// suggests: the weights still go up per exercise, and adding a round is the
// athlete's call in the editor.
func suggestRounds(ex []store.Exercise, hitWhen []string) {
	for _, c := range store.Circuits(ex) {
		if c.Rounds(ex) >= store.MaxSetsPerLift {
			continue
		}
		all := true
		for i := c.Start; i < c.End; i++ {
			all = all && hitWhen[i] != ""
		}
		if !all {
			continue
		}
		note := "Every exercise in the circuit hit last time: add a round if it felt easy"
		if ex[c.Start].Change != "" {
			note = ex[c.Start].Change + ". " + note
		}
		ex[c.Start].Change = note
	}
}

// MinSetSeconds is the shortest set that counts as lifting. Anything quicker
// is a start-and-finish tap, not a set.
const MinSetSeconds = 5

// SecondsPerRep is the fastest believable pace: a set logged faster than this
// was tapped through, whatever the reps say.
const SecondsPerRep = 2

// Real reports whether a logged set was plausibly lifted.
func Real(s store.Set) bool {
	min := int64(max(MinSetSeconds, SecondsPerRep*s.Reps)) * 1000
	return s.EndedAt-s.StartedAt >= min
}

// Counted drops sets too short to be real.
func Counted(sets []store.Set) []store.Set {
	out := sets[:0:0]
	for _, s := range sets {
		if Real(s) {
			out = append(out, s)
		}
	}
	return out
}

// Progress is how much of a session's plan was actually lifted.
type Progress struct {
	Done    int `json:"done"`    // sets that were really lifted
	Logged  int `json:"logged"`  // every set logged, taps included
	Planned int `json:"planned"`
}

// Complete is true when at least half the planned sets were lifted: enough
// to call today's session done.
func (p Progress) Complete() bool { return p.Planned == 0 || p.Done*2 >= p.Planned }

// ProgressOf counts real sets against the plan.
func ProgressOf(plan []store.Exercise, sets []store.Set) Progress {
	p := Progress{Done: len(Counted(sets)), Logged: len(sets)}
	for _, e := range plan {
		p.Planned += len(e.Sets)
	}
	return p
}

// CircuitShare is the fraction of a session's working time (first set's
// start to last set's end) spent in circuits: from each circuit's first set
// to its last. Sets too short to be real don't count.
func CircuitShare(plan []store.Exercise, sets []store.Set) float64 {
	real := Counted(sets)
	if len(real) == 0 {
		return 0
	}
	from, to := real[0].StartedAt, real[0].EndedAt
	for _, s := range real {
		from, to = min(from, s.StartedAt), max(to, s.EndedAt)
	}
	if to <= from {
		return 0
	}
	var in int64
	for _, c := range store.Circuits(plan) {
		var cf, ct int64
		for _, s := range real {
			if s.ExerciseIndex < c.Start || s.ExerciseIndex >= c.End {
				continue
			}
			if cf == 0 || s.StartedAt < cf {
				cf = s.StartedAt
			}
			ct = max(ct, s.EndedAt)
		}
		if ct > cf {
			in += ct - cf
		}
	}
	return math.Round(math.Min(1, float64(in)/float64(to-from))*100) / 100
}

func hitAll(sets []store.Set) bool {
	for _, s := range sets {
		if s.Reps < s.TargetReps {
			return false
		}
	}
	return len(sets) > 0
}

func roundTo(v, step float64) float64 { return math.Round(v/step) * step }

func fmtKg(v float64) string {
	if v == math.Trunc(v) {
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.1f", v)
}

func fmtLoad(kg float64) string {
	if kg == 0 {
		return "bodyweight"
	}
	return fmtKg(kg) + " kg"
}

// E1RM estimates a one-rep max (Epley), meaningful up to about 10 reps.
func E1RM(reps int, kg float64) float64 {
	if reps <= 0 || kg <= 0 {
		return 0
	}
	if reps == 1 {
		return kg
	}
	return math.Round(kg*(1+float64(reps)/30)*10) / 10
}
