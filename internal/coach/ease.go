package coach

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

// When readiness says hold back and a lifting session is planned, the LLM
// proposes an easier version of today's prescription. It's a proposal: the
// athlete takes it or keeps the plan, and taking it changes only today.

// EaseSet is one set of a plan.
type EaseSet struct {
	Reps int     `json:"reps"`
	Kg   float64 `json:"kg"`
}

// EaseExercise is one exercise of a plan.
type EaseExercise struct {
	Name string    `json:"name"`
	Sets []EaseSet `json:"sets"`
}

// EaseFacts is what the easier session is written from.
type EaseFacts struct {
	Date      string         `json:"date"`
	Level     string         `json:"level"` // hold | easy, from the rules
	Why       string         `json:"rules_call"`
	Recovery  *int           `json:"recovery_pct,omitempty"`
	HRVms     *int           `json:"hrv_ms,omitempty"`
	RestingHR *int           `json:"resting_hr,omitempty"`
	SleepH    *float64       `json:"sleep_hours,omitempty"`
	Form      *int           `json:"form_tsb,omitempty"`
	Session   string         `json:"planned_session"`
	Plan      []EaseExercise `json:"planned"`
}

// Ease is the proposed session and the reason for it.
type Ease struct {
	Reason    string         `json:"reason"`
	Exercises []EaseExercise `json:"exercises"`
	Model     string         `json:"model,omitempty"`
}

const easePrompt = `You are the directeur sportif for one athlete, a cyclist who also lifts. Recovery says to hold back today, and a lifting session is planned. Propose an easier version of the planned session, and give the reason in one sentence.

Rules for the session:
- Keep every exercise, with the same name, in the same order.
- You may drop sets (keep at least one per exercise), lower the weight, or lower the reps. Never add sets, reps or weight.
- With level "easy", cut about a third of the work. With level "hold", trim lightly: one set fewer on the heaviest lifts, or about 10% off their weight.
- Weights in kg, in steps of 2.5. A weight of 0 is bodyweight: leave it at 0.

The reason: what you changed and why, using at most two numbers from the facts. ` + voice + `

Reply with only a JSON object, no other text:
{"reason": "<one sentence, at most 180 characters>", "exercises": [{"name": "<as planned>", "sets": [{"reps": <n>, "kg": <n>}]}]}`

// Ease returns the proposed easier session for these facts, or starts
// writing one.
func (w *Writer) Ease(ctx context.Context, f EaseFacts) (e Ease, ok, pending bool) {
	ok, pending = w.Do(ctx, "ai:ease:"+f.Date, Fingerprint(easePrompt, f), &e,
		func(ctx context.Context) (any, error) { return w.ease(ctx, f) })
	return e, ok, pending
}

func (w *Writer) ease(ctx context.Context, f EaseFacts) (Ease, error) {
	in, _ := json.MarshalIndent(f, "", "  ")
	text, model, err := w.AI.Chat(ctx, easePrompt, "Facts:\n"+string(in))
	if err != nil {
		return Ease{}, err
	}
	var e Ease
	if err := parseObject(text, &e); err != nil {
		return Ease{}, fmt.Errorf("%w (model %s)", err, model)
	}
	if e.Exercises, err = CheckEase(f.Plan, e.Exercises); err != nil {
		return Ease{}, fmt.Errorf("%w (model %s)", err, model)
	}
	e.Reason = strings.Join(strings.Fields(e.Reason), " ")
	// The reason may name the new weights, so they count as known too.
	if err := checkProse(e.Reason, 200, struct {
		F EaseFacts
		E []EaseExercise
	}{f, e.Exercises}); err != nil {
		return Ease{}, fmt.Errorf("%w (model %s)", err, model)
	}
	if n := figures(e.Reason); n > 3 {
		return Ease{}, fmt.Errorf("recites %d numbers (model %s): %q", n, model, clip(e.Reason))
	}
	e.Model = model
	return e, nil
}

// CheckEase checks that got is plan made easier: the same exercises in the
// same order, at least one set each, no set, rep or kilo added, and less
// work overall. It returns got with the plan's names, and weights rounded
// down to 0.5 kg.
func CheckEase(plan, got []EaseExercise) ([]EaseExercise, error) {
	if len(got) != len(plan) {
		return nil, fmt.Errorf("easier session has %d exercises, the plan %d", len(got), len(plan))
	}
	out := make([]EaseExercise, len(plan))
	var before, after float64
	work := func(s EaseSet) float64 { return float64(s.Reps) * math.Max(s.Kg, 1) }
	for i, p := range plan {
		g := got[i]
		if !strings.EqualFold(strings.TrimSpace(g.Name), p.Name) {
			return nil, fmt.Errorf("exercise %d is %q, the plan's is %q", i+1, g.Name, p.Name)
		}
		if len(g.Sets) == 0 || len(g.Sets) > len(p.Sets) {
			return nil, fmt.Errorf("%s: %d sets, the plan has %d", p.Name, len(g.Sets), len(p.Sets))
		}
		out[i] = EaseExercise{Name: p.Name, Sets: make([]EaseSet, len(g.Sets))}
		for j, s := range g.Sets {
			ps := p.Sets[j]
			kg := math.Floor(s.Kg*2) / 2
			if s.Reps < 1 || s.Reps > ps.Reps || kg < 0 || kg > ps.Kg {
				return nil, fmt.Errorf("%s set %d: %d × %g kg, the plan %d × %g kg", p.Name, j+1, s.Reps, s.Kg, ps.Reps, ps.Kg)
			}
			out[i].Sets[j] = EaseSet{Reps: s.Reps, Kg: kg}
			after += work(out[i].Sets[j])
		}
		for _, s := range p.Sets {
			before += work(s)
		}
	}
	if after >= before {
		return nil, fmt.Errorf("easier session is no easier")
	}
	return out, nil
}
