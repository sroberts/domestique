package eval

import (
	"context"
	"encoding/json"

	"domestique/internal/coach"
)

// Today's "feeling run down?" prompt (internal/api/today_ai.go).
func init() {
	Register(Question{Name: "sick", Bar: coach.Bar("sick"), Ask: func(ctx context.Context, ai *coach.AI, facts json.RawMessage) (map[string]Lean, error) {
		var f coach.SickFacts
		if err := json.Unmarshal(facts, &f); err != nil {
			return nil, err
		}
		return single(ai.Sick(ctx, f))
	}})
}
