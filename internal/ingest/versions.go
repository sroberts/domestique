package ingest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"

	"domestique/internal/activity"
	"domestique/internal/store"
)

// MergeRule names the current merge rule. Remerge runs once per rule, so
// recordings imported under an older, stricter rule get folded together, and
// activities an older rule merged wrongly get rebuilt.
const MergeRule = "overlap-80+whoop-no-venue+fit-climbs"

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
	resported := map[string]bool{}
	for i := range srcs {
		ok, err := in.resport(ctx, &srcs[i])
		if err != nil {
			return nil, err
		}
		if ok {
			resported[srcs[i].ActivityID] = true
		}
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

	// Older rules let Whoop, or any GPS-less indoor guess, turn an outdoor
	// ride indoor. Rebuild every ride where that could have happened.
	whoop, guess, other, outdoor := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, s := range srcs {
		if !activity.IsRide(s.Sport) {
			continue
		}
		id := where(s.ActivityID)
		if s.Source == "whoop" {
			whoop[id] = true
		} else {
			other[id] = true
		}
		switch {
		case s.Sport == activity.SportVirtualRide && !s.HasGPS:
			guess[id] = true
		case s.Sport == activity.SportRide:
			outdoor[id] = true
		}
	}
	fixed := map[string]bool{}
	for id := range resported {
		fixed[where(id)], other[where(id)] = true, true
	}
	for id := range other {
		stale := whoop[id] || guess[id] && outdoor[id] || fixed[id]
		if !stale || slices.Contains(changed, id) {
			continue
		}
		if err := in.Rebuild(ctx, id); err != nil {
			return changed, err
		}
		changed = append(changed, id)
	}
	return changed, nil
}

// resport re-reads an archived FIT ride stored as indoor for want of GPS, and
// stores it as outdoor if the file now says so (an outdoor sub-sport, or
// the altimeter shows real climbing). It reports whether it changed.
func (in *Ingester) resport(ctx context.Context, s *store.Source) (bool, error) {
	if s.Sport != activity.SportVirtualRide || s.HasGPS || s.RawSHA256 == nil {
		return false, nil
	}
	raw, err := in.Store.GetRaw(ctx, *s.RawSHA256)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	if raw.Format != "fit" {
		return false, nil
	}
	data, err := os.ReadFile(filepath.Join(in.StorageDir, raw.Path))
	if err != nil {
		in.Log.Warn("re-sport: raw file unreadable", "source", s.ID, "err", err.Error())
		return false, nil
	}
	_, plain, err := decode(raw.Path, data)
	if err != nil {
		return false, nil
	}
	p, err := activity.ParseFIT(plain)
	if err != nil || p.Sport != activity.SportRide {
		return false, nil
	}
	s.Sport = activity.SportRide
	if err := in.Store.PutSource(ctx, *s); err != nil {
		return false, err
	}
	in.Log.Info("re-sport: outdoor after all", "source", s.ID)
	return true, nil
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
