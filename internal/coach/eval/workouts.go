package eval

import (
	"context"
	"encoding/json"

	"domestique/internal/coach"
)

// The sweep's questions about workouts (internal/sweep) and set effort.
func init() {
	yesNo := func(name string, ask func(*coach.AI, context.Context, coach.WorkoutFacts) (float64, error)) {
		Register(Question{Name: name, Bar: coach.Bar(name), Ask: func(ctx context.Context, ai *coach.AI, facts json.RawMessage) (map[string]Lean, error) {
			var f coach.WorkoutFacts
			if err := json.Unmarshal(facts, &f); err != nil {
				return nil, err
			}
			p, err := ask(ai, ctx, f)
			return single(p, err)
		}})
	}
	yesNo("indoor", (*coach.AI).Indoor)
	yesNo("stray", (*coach.AI).Stray)

	Register(Question{Name: "same", Bar: coach.Bar("same"), Ask: func(ctx context.Context, ai *coach.AI, facts json.RawMessage) (map[string]Lean, error) {
		var f struct{ A, B coach.WorkoutFacts }
		if err := json.Unmarshal(facts, &f); err != nil {
			return nil, err
		}
		return single(ai.SameWorkout(ctx, f.A, f.B))
	}})

	Register(Question{Name: "sport", Bar: coach.Bar("sport"), Ask: func(ctx context.Context, ai *coach.AI, facts json.RawMessage) (map[string]Lean, error) {
		var f coach.WorkoutFacts
		if err := json.Unmarshal(facts, &f); err != nil {
			return nil, err
		}
		s, p, err := ai.SportGuess(ctx, f)
		return map[string]Lean{"": {s, p}}, err
	}})

	Register(Question{Name: "effort", Bar: coach.EffortFloor, Ask: func(ctx context.Context, ai *coach.AI, facts json.RawMessage) (map[string]Lean, error) {
		var sets map[string]coach.SetState
		if err := json.Unmarshal(facts, &sets); err != nil {
			return nil, err
		}
		got, err := ai.SetEffortsAll(ctx, sets)
		out := map[string]Lean{}
		for id, e := range got {
			out[id] = Lean{e.Level, e.Confidence} // none given: not shown
		}
		return out, err
	}})
}

// single is a yes/no answer: the probability of yes, or -1 for none.
func single(p float64, err error) (map[string]Lean, error) {
	if err != nil || p < 0 {
		return map[string]Lean{"": {}}, err
	}
	return map[string]Lean{"": {"yes", p}}, nil
}
