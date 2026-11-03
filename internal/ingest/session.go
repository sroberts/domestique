package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"domestique/internal/activity"
	"domestique/internal/store"
)

// UpsertSession mirrors a finished strength session from the app into the
// activity store, as an "app" source whose HR stream is sliced from whatever
// heart rate exists for that window (Whoop). A Whoop workout recorded over the
// same time later merges into the same activity.
func (in *Ingester) UpsertSession(ctx context.Context, sessionID string) error {
	in.mu.Lock()
	defer in.mu.Unlock()

	sess, err := in.Store.GetSession(ctx, sessionID)
	if err != nil {
		return err
	}
	if sess.FinishedAt == nil {
		return nil
	}
	sets, err := in.Store.SessionSets(ctx, sessionID)
	if err != nil {
		return err
	}
	srcID := "app:" + sessionID
	elapsed := int((*sess.FinishedAt - sess.StartedAt) / 1000)

	extra := map[string]any{"sets": len(sets)}
	var volume float64
	for _, s := range sets {
		volume += float64(s.Reps) * s.WeightKg
	}
	extra["volume_kg"] = volume
	if sess.Strain != nil {
		extra["strain"] = *sess.Strain
	}
	extraJSON, _ := json.Marshal(extra)

	// Slice heart rate for the session window into a stream.
	hr, err := in.Store.HR(ctx, sess.StartedAt, *sess.FinishedAt)
	if err != nil {
		return err
	}
	var st activity.Stream
	for _, h := range hr {
		t := int32((h.T - sess.StartedAt) / 1000)
		if n := len(st.T); n > 0 && t <= st.T[n-1] {
			continue // sub-second samples, or two sources at once: one per second
		}
		st.T = append(st.T, t)
		st.HR = append(st.HR, int32(h.BPM))
	}

	actID := ""
	if prev, err := in.Store.GetSource(ctx, srcID); err == nil {
		actID = prev.ActivityID
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	} else if a, err := in.Store.ActivityBySession(ctx, sessionID); err == nil {
		actID = a.ID
	} else {
		src := store.Source{ID: srcID, Sport: activity.SportStrength, StartAt: sess.StartedAt, ElapsedS: elapsed}
		if id, _, err := in.match(ctx, src, &st); err != nil {
			return err
		} else {
			actID = id
		}
	}
	if actID == "" {
		actID = "s_" + sessionID
		if err := in.Store.PutActivity(ctx, store.Activity{ID: actID, Sport: activity.SportStrength, Name: sess.Name,
			StartAt: sess.StartedAt, ElapsedS: elapsed, MovingS: elapsed, SessionID: &sessionID}); err != nil {
			return err
		}
	}
	src := store.Source{
		ID: srcID, ActivityID: actID, Source: "app", ExternalID: sessionID,
		Sport: activity.SportStrength, Name: sess.Name, StartAt: sess.StartedAt,
		ElapsedS: elapsed, MovingS: elapsed, HasHR: st.HasHR(), Extra: extraJSON,
		ImportedAt: time.Now().UnixMilli(),
	}
	if err := in.Store.PutSource(ctx, src); err != nil {
		return err
	}
	if st.Len() > 0 {
		if err := in.Store.PutStream(ctx, srcID, st); err != nil {
			return err
		}
	}
	return in.Rebuild(ctx, actID)
}

// RemoveSession drops a discarded session from the activity store. If Whoop
// also recorded the workout, that activity stays, without the app's sets.
func (in *Ingester) RemoveSession(ctx context.Context, sessionID string) error {
	in.mu.Lock()
	defer in.mu.Unlock()
	srcID := "app:" + sessionID
	src, err := in.Store.GetSource(ctx, srcID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	if err := in.Store.DeleteSource(ctx, srcID); err != nil {
		return err
	}
	if err := in.Store.ClearSession(ctx, src.ActivityID); err != nil {
		return err
	}
	return in.Rebuild(ctx, src.ActivityID)
}
