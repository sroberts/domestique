package ingest

import (
	"encoding/json"

	"domestique/internal/activity"
	"domestique/internal/store"
)

// BadExtra is the source extra holding channels System 1 judged broken
// (internal/sweep), each with a few words on why: {"hr": "strap dropouts"}.
// Those channels are left out of the merged stream, so zones, max heart
// rate and load come from another recording's, or go without.
const BadExtra = "ai_bad"

// BadChannels reads a source's broken channels.
func BadChannels(extra []byte) map[string]string {
	var ex struct {
		Bad map[string]string `json:"ai_bad"`
	}
	_ = json.Unmarshal(extra, &ex)
	return ex.Bad
}

// dropBad empties the channels of st that src's extra marks broken.
func dropBad(src store.Source, st *activity.Stream) {
	for ch := range BadChannels(src.Extra) {
		switch ch {
		case "hr":
			st.HR = nil
		case "power":
			st.Power = nil
		}
	}
}
