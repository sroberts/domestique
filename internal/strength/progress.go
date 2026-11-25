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
	// Source is "llm" when the operator's model wrote Title and Detail (the
	// level is always the rules'), and Pending that it is writing them now.
	Source  string `json:"source,omitempty"`
	Pending bool   `json:"pending,omitempty"`
}

func call(level, title, detail string) Readiness {
	return Readiness{Level: level, Title: title, Detail: detail}
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
		// after one set, or one tapped through in seconds, says nothing about
		// whether the weight should go up. A quick set among real ones still
		// counts: it was a slow tap on Finish, not a missing set.
		var hist [][]store.Set
		for _, h := range raw {
			if len(h) > 0 && len(h)*2 >= len(ex.Sets) && !Tapped(h) {
				hist = append(hist, h)
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
	out.Circuits = store.CircuitSpecs(out.Exercises)
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
		note := "Every exercise in the circuit hit last time: add a round at the end if it feels easy"
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

// Tapped reports a session tapped through rather than lifted: sets were
// logged, and every one of them was too quick to be real. One quick set in
// a session that was lifted is a slow tap on Finish, not a fake set.
func Tapped(sets []store.Set) bool {
	return len(sets) > 0 && len(Counted(sets)) == 0
}

// Progress is how much of a session's plan was logged.
type Progress struct {
	Done    int `json:"done"`  // every set logged
	Short   int `json:"short"` // of those, timed too quick to be real
	Planned int `json:"planned"`
}

// Complete is true when at least half the planned sets were done: enough
// to call today's session done.
func (p Progress) Complete() bool { return !p.Empty() && (p.Planned == 0 || p.Done*2 >= p.Planned) }

// Empty is true when nothing was lifted: no sets, or only taps.
func (p Progress) Empty() bool { return p.Done == 0 || p.Short == p.Done }

// ProgressOf counts logged sets against the plan.
func ProgressOf(plan []store.Exercise, sets []store.Set) Progress {
	p := Progress{Done: len(sets), Short: len(sets) - len(Counted(sets))}
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
