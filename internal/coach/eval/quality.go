package eval

import (
	"context"
	"encoding/json"

	"domestique/internal/coach"
)

// The sweep's questions about the data itself (internal/sweep/quality.go).
func init() {
	Register(Question{Name: "bad_data", Bar: coach.Bar("bad_data"), Ask: func(ctx context.Context, ai *coach.AI, facts json.RawMessage) (map[string]Lean, error) {
		var f coach.RecordingFacts
		if err := json.Unmarshal(facts, &f); err != nil {
			return nil, err
		}
		// Only suspect channels are ever asked about, as in the sweep.
		hr, pw, err := ai.BadData(ctx, coach.Suspect(f))
		out := map[string]Lean{}
		for id, p := range map[string]float64{"hr": hr, "power": pw} {
			if p >= 0 {
				out[id] = Lean{"yes", p}
			}
		}
		return out, err
	}})

	Register(Question{Name: "ride_type", Bar: coach.Bar("ride_type"), Ask: func(ctx context.Context, ai *coach.AI, facts json.RawMessage) (map[string]Lean, error) {
		var f coach.RideFacts
		if err := json.Unmarshal(facts, &f); err != nil {
			return nil, err
		}
		t, p, err := ai.RideTypeGuess(ctx, f)
		return map[string]Lean{"": {t, p}}, err
	}})
}
