package eval

import (
	"context"
	"encoding/json"

	"domestique/internal/coach"
)

// Exercise names: a new name against the athlete's names, as the sweep
// asks it (internal/sweep/exercises.go). A case's known list is what
// Nearest would offer.
func init() {
	Register(Question{Name: "exercise", Bar: coach.Bar("exercise"), Ask: func(ctx context.Context, ai *coach.AI, facts json.RawMessage) (map[string]Lean, error) {
		var f struct {
			Name  string   `json:"name"`
			Known []string `json:"known"`
		}
		if err := json.Unmarshal(facts, &f); err != nil {
			return nil, err
		}
		k, p, err := ai.SameExercise(ctx, f.Name, coach.Nearest(f.Name, f.Known, coach.MaxKnown))
		return map[string]Lean{"": {k, p}}, err
	}})
}
