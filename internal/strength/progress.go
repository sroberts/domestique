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
	for i, ex := range t.Exercises {
		ex.Sets = append([]store.TargetSet(nil), ex.Sets...)
		hist, err := st.ExerciseHistory(ctx, ex.Name, 2)
		if err != nil {
			return t, err
		}
		change := ""
		if len(hist) > 0 && len(hist[0]) > 0 {
			last := hist[0]
			lastW := 0.0
			for _, s := range last {
				lastW = math.Max(lastW, s.WeightKg)
			}
			when := time.UnixMilli(last[0].StartedAt).Format("Mon 2 Jan")
			hit := hitAll(last)
			missedTwice := !hit && len(hist) > 1 && !hitAll(hist[1])
			bodyweight := lastW == 0
			targetReps := ex.Sets[0].Reps
			w := lastW
			switch {
			case missedTwice && !bodyweight:
				w = roundTo(lastW*0.9, 2.5)
				change = fmt.Sprintf("Back off to %s kg: missed reps two sessions running", fmtKg(w))
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
	return out, nil
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
