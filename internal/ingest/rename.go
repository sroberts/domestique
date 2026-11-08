package ingest

import (
	"context"
	"encoding/json"
)

// NameExtra is the source extra holding a name the athlete gave a workout in
// Domestique. It's set on every source of the activity, so it survives a
// rebuild, and a merge or split keeps it with the recordings. It's never
// sent back to Strava or anywhere else.
const NameExtra = "name"

// NameOf reads a source's renamed name, or "".
func NameOf(extra []byte) string {
	var ex struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(extra, &ex)
	return ex.Name
}

// Rename names an activity in Domestique only.
func (in *Ingester) Rename(ctx context.Context, activityID, name string) error {
	srcs, err := in.Store.SourcesFor(ctx, activityID)
	if err != nil {
		return err
	}
	for _, s := range srcs {
		if err := in.Store.SetSourceExtra(ctx, s.ID, NameExtra, name); err != nil {
			return err
		}
	}
	return in.Rebuild(ctx, activityID)
}
