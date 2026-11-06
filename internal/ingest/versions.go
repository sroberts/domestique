package ingest

import (
	"context"
	"errors"

	"domestique/internal/activity"
	"domestique/internal/store"
)

// MergeRule names the current matching rule. Remerge runs once per rule, so
// recordings imported under an older, stricter rule get folded together.
const MergeRule = "overlap-80"

// Remerge walks every source and folds activities that the matcher now says
// are the same workout. It returns the activities it changed, rebuilt but
// not yet load-scored.
func (in *Ingester) Remerge(ctx context.Context) ([]string, error) {
	in.mu.Lock()
	defer in.mu.Unlock()
	srcs, err := in.Store.AllSources(ctx)
	if err != nil {
		return nil, err
	}
	moved := map[string]string{} // activity folded away -> where it went
	where := func(id string) string {
		for {
			to, ok := moved[id]
			if !ok {
				return id
			}
			id = to
		}
	}
	var changed []string
	for _, s := range srcs {
		if err := ctx.Err(); err != nil {
			return changed, err
		}
		s.ActivityID = where(s.ActivityID)
		var st activity.Stream
		if s.HasHR {
			if err := in.Store.GetStream(ctx, s.ID, &st); err != nil && !errors.Is(err, store.ErrNotFound) {
				return changed, err
			}
		}
		to, ok, err := in.match(ctx, s, &st)
		if err != nil {
			return changed, err
		}
		to = where(to)
		if !ok || to == s.ActivityID {
			continue
		}
		from := s.ActivityID
		// Keep the activity a logged lifting session hangs off, so its
		// summary link stays put.
		// Two logged sessions can't share one activity: MergeActivities keeps
		// only one session link, stranding the other's summary.
		if a, err := in.Store.GetActivity(ctx, from); err == nil && a.SessionID != nil {
			b, err := in.Store.GetActivity(ctx, to)
			if err == nil && b.SessionID != nil {
				continue
			}
			if err == nil {
				from, to = to, from
			}
		}
		if err := in.Store.MergeActivities(ctx, from, to); err != nil {
			return changed, err
		}
		moved[from] = to
		if err := in.Rebuild(ctx, to); err != nil {
			return changed, err
		}
		changed = append(changed, to)
	}
	return changed, nil
}

// Version is one source's own account of an activity, before merging.
type Version struct {
	Source  store.Source
	Metrics activity.Metrics
}

// Versions returns each source's own metrics, and the stream to show: the
// merged one, or only's alone when only names one of the sources.
func (in *Ingester) Versions(ctx context.Context, actID, only string) (*Merged, []Version, error) {
	ls, err := in.loadSources(ctx, actID)
	if err != nil {
		return nil, nil, err
	}
	vs := make([]Version, len(ls))
	var pickd []loaded
	for i, l := range ls {
		vs[i].Source = l.src
		if l.stream != nil {
			vs[i].Metrics = activity.Compute(l.stream)
		}
		if l.src.ID == only {
			pickd = append(pickd, l)
		}
	}
	if only != "" && len(pickd) == 0 {
		return nil, nil, store.ErrNotFound
	}
	if only == "" {
		pickd = ls
	}
	return merge(pickd), vs, nil
}
