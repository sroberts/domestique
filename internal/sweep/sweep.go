// Package sweep goes over stored workouts in the background and asks the
// operator's models (Outpost service spec §15) what rules can't tell:
//
//   - System 1: the sport of a workout labelled "other"; whether a ride with
//     no GPS was indoors; whether two overlapping recordings the matcher kept
//     apart are one workout; whether an entry typed in by hand is a stray;
//     whether a recording's heart rate or power is broken, and what kind of
//     ride each ride was (quality.go); whether a new exercise name is one
//     the athlete already logs.
//   - The LLM: tags and a note from the athlete's own descriptions, and
//     the bike parts they wrote about fitting (gear.go).
//
// Each question is asked once per set of facts and its answer kept in
// sync_state, so a sweep after the first costs nothing until new workouts
// arrive. Data changes only on a confident answer (coach.Bar).
package sweep

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"regexp"
	"strings"
	"sync"
	"time"

	"domestique/internal/activity"
	"domestique/internal/coach"
	"domestique/internal/ingest"
	"domestique/internal/load"
	"domestique/internal/store"
)

// Sweeper runs the passes. Run is safe to call often; one sweep runs at a time.
type Sweeper struct {
	W      *coach.Writer
	Store  *store.Store
	Ingest *ingest.Ingester
	Log    *slog.Logger
	Loc    *time.Location
	// Recalc asks for load to be recomputed, after a sweep changed something.
	Recalc func()

	mu sync.Mutex // held while a sweep runs
}

// Run sweeps once, unless a sweep is already running.
func (s *Sweeper) Run(ctx context.Context) {
	if !s.mu.TryLock() {
		return
	}
	defer s.mu.Unlock()
	changed := false
	for _, pass := range []struct {
		name string
		run  func(context.Context) (bool, error)
	}{
		{"same workout", s.sameWorkout},
		{"sport", s.sports},
		{"indoor", s.indoor},
		{"stray", s.strays},
		{"tags", s.tags},
		{"gear", s.gearParts},
		{"bad data", s.badData},
		{"ride type", s.rideTypes},
		{"exercise names", s.exerciseNames},
	} {
		c, err := pass.run(ctx)
		changed = changed || c
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			// One unavailable model shouldn't stop the passes using the other.
			s.W.LogFailure(ctx, "sweep: "+pass.name, err)
		}
	}
	if changed && s.Recalc != nil {
		s.Recalc()
	}
}

// stop reports whether a pass should end on err: when the model is
// unavailable, the endpoint is missing, or the sweep is cancelled. Anything else is one bad answer,
// logged and skipped, so one item can't hold up the rest.
func (s *Sweeper) stop(ctx context.Context, err error) bool {
	if ctx.Err() != nil || !coach.OneOff(err) {
		return true
	}
	s.Log.Warn("sweep: skipped one", "err", err.Error())
	return false
}

// asked runs a question once per facts: it returns the kept answer when
// these facts were asked before, or asks and keeps the new one.
func asked[T any](ctx context.Context, s *Sweeper, key string, facts any, ask func() (T, error)) (T, bool, error) {
	fp := coach.Fingerprint("v1", facts)
	var v T
	if s.W.Cached(ctx, key, fp, &v) {
		return v, false, nil
	}
	v, err := ask()
	if err != nil {
		return v, false, err
	}
	return v, true, s.W.Keep(ctx, key, fp, v)
}

func (s *Sweeper) facts(a store.Activity, srcs []store.Source) coach.WorkoutFacts {
	loc := s.Loc
	if loc == nil {
		loc = time.Local
	}
	f := coach.WorkoutFacts{Name: a.Name, Start: time.UnixMilli(a.StartAt).In(loc).Format("Mon 15:04"),
		Minutes: int(math.Round(float64(a.ElapsedS) / 60))}
	if a.DistanceM > 0 {
		km := math.Round(a.DistanceM/100) / 10
		f.KM = &km
		if a.MovingS > 0 {
			v := math.Round(a.DistanceM/float64(a.MovingS)*36) / 10
			f.AvgSpeed = &v
		}
	}
	var m activity.Metrics
	_ = json.Unmarshal(a.Metrics, &m)
	if m.AvgHR > 0 {
		f.AvgHR = &m.AvgHR
	}
	if m.AvgCadence > 0 {
		f.Cadence = &m.AvgCadence
	}
	f.Strain = a.Strain
	f.Manual = true
	var sources []string
	for _, src := range srcs {
		sources = append(sources, src.Source)
		if src.Device != "" && f.Device == "" {
			f.Device = src.Device
		}
		f.HasGPS = f.HasGPS || src.HasGPS
		f.HasPower = f.HasPower || src.HasPower
		f.HasHR = f.HasHR || src.HasHR
		if src.RawSHA256 != nil || src.Source != "strava" {
			f.Manual = false
		}
		if d := Description(src); d != "" && f.Notes == "" {
			f.Notes = d
		}
	}
	f.Source = strings.Join(sources, "+")
	return f
}

func (s *Sweeper) activities(ctx context.Context, keep func(store.Activity) bool) ([]store.Activity, map[string][]store.Source, error) {
	all, err := s.Store.ListActivities(ctx, store.ActivityQuery{})
	if err != nil {
		return nil, nil, err
	}
	var acts []store.Activity
	var ids []string
	for _, a := range all {
		if keep(a) {
			acts = append(acts, a)
			ids = append(ids, a.ID)
		}
	}
	srcs, err := s.Store.SourcesForMany(ctx, ids)
	return acts, srcs, err
}

// sports asks System 1 which sport an "other" workout was, and records a
// confident answer on its "other" sources for Rebuild to use.
func (s *Sweeper) sports(ctx context.Context) (bool, error) {
	acts, srcs, err := s.activities(ctx, func(a store.Activity) bool { return a.Sport == activity.SportOther })
	if err != nil {
		return false, err
	}
	changed := false
	for _, a := range acts {
		f := s.facts(a, srcs[a.ID])
		type verdict struct {
			Sport string `json:"sport"`
		}
		v, fresh, err := asked(ctx, s, "ai:sport:"+a.ID, f, func() (verdict, error) {
			sp, ok, err := s.W.AI.Sport(ctx, f)
			if !ok {
				sp = ""
			}
			return verdict{sp}, err
		})
		if err != nil {
			if s.stop(ctx, err) {
				return changed, err
			}
			continue
		}
		if !fresh || v.Sport == "" {
			continue
		}
		for _, src := range srcs[a.ID] {
			if src.Sport == activity.SportOther {
				if err := s.Store.SetSourceExtra(ctx, src.ID, "ai_sport", v.Sport); err != nil {
					return changed, err
				}
			}
		}
		if err := s.Ingest.Rebuild(ctx, a.ID); err != nil {
			return changed, err
		}
		s.Log.Info("sweep: sport from System 1", "activity", a.ID, "name", a.Name, "sport", v.Sport)
		changed = true
	}
	return changed, nil
}

// indoor asks System 1 whether a ride with no GPS was on a trainer.
func (s *Sweeper) indoor(ctx context.Context) (bool, error) {
	acts, srcs, err := s.activities(ctx, func(a store.Activity) bool { return a.Sport == activity.SportRide })
	if err != nil {
		return false, err
	}
	changed := false
	for _, a := range acts {
		f := s.facts(a, srcs[a.ID])
		if f.HasGPS {
			continue // outside, and the map shows it
		}
		if ingest.FromFile(srcs[a.ID]) {
			// A recording file only reads as outdoor without GPS on evidence
			// (an outdoor sub-sport, a climb, the athlete's label), which the
			// model doesn't see. The evidence stands.
			continue
		}
		type verdict struct {
			P float64 `json:"p"`
		}
		v, fresh, err := asked(ctx, s, "ai:indoor:"+a.ID, f, func() (verdict, error) {
			p, err := s.W.AI.Indoor(ctx, f)
			return verdict{p}, err
		})
		if err != nil {
			if s.stop(ctx, err) {
				return changed, err
			}
			continue
		}
		if !fresh || v.P < coach.Bar("indoor") {
			continue
		}
		for _, src := range srcs[a.ID] {
			if err := s.Store.SetSourceExtra(ctx, src.ID, "ai_indoor", true); err != nil {
				return changed, err
			}
		}
		if err := s.Ingest.Rebuild(ctx, a.ID); err != nil {
			return changed, err
		}
		s.Log.Info("sweep: indoor ride from System 1", "activity", a.ID, "name", a.Name)
		changed = true
	}
	return changed, nil
}

// sameWorkout asks System 1 about recordings that overlap enough to be one
// workout but that heart rate kept apart, and merges a confident yes.
func (s *Sweeper) sameWorkout(ctx context.Context) (bool, error) {
	pairs, err := s.Ingest.Unmerged(ctx)
	if err != nil {
		return false, err
	}
	changed := false
	for _, p := range pairs {
		x, err1 := s.Store.GetActivity(ctx, p.A.ActivityID)
		y, err2 := s.Store.GetActivity(ctx, p.B.ActivityID)
		if errors.Is(err1, store.ErrNotFound) || errors.Is(err2, store.ErrNotFound) {
			continue // merged earlier in this pass
		}
		if err := errors.Join(err1, err2); err != nil {
			return changed, err
		}
		fx, fy := s.facts(x, []store.Source{p.A}), s.facts(y, []store.Source{p.B})
		type verdict struct {
			P float64 `json:"p"`
		}
		key := "ai:same:" + min(p.A.ID, p.B.ID) + "|" + max(p.A.ID, p.B.ID)
		v, fresh, err := asked(ctx, s, key, [2]coach.WorkoutFacts{fx, fy}, func() (verdict, error) {
			pr, err := s.W.AI.SameWorkout(ctx, fx, fy)
			return verdict{pr}, err
		})
		if err != nil {
			if s.stop(ctx, err) {
				return changed, err
			}
			continue
		}
		if !fresh || v.P < coach.Bar("same") {
			continue
		}
		to, err := s.Ingest.MergePair(ctx, x.ID, y.ID)
		if err != nil {
			s.Log.Warn("sweep: merge", "a", x.ID, "b", y.ID, "err", err.Error())
			continue
		}
		s.Log.Info("sweep: merged on System 1's word", "into", to, "a", x.Name, "b", y.Name, "p", v.P)
		changed = true
	}
	return changed, nil
}

// strays asks System 1 whether an entry typed in by hand on Strava, with no
// file behind it and nothing else recording it, is a real workout.
func (s *Sweeper) strays(ctx context.Context) (bool, error) {
	acts, srcs, err := s.activities(ctx, func(store.Activity) bool { return true })
	if err != nil {
		return false, err
	}
	kept, err := s.Store.SyncStatePrefix(ctx, load.StrayPrefix)
	if err != nil {
		return false, err
	}
	changed := false
	for _, a := range acts {
		ss := srcs[a.ID]
		if len(ss) != 1 || ss[0].Source != "strava" || ss[0].RawSHA256 != nil || kept[a.ID] == "kept" {
			continue
		}
		f := s.facts(a, ss)
		type verdict struct {
			P float64 `json:"p"`
		}
		v, fresh, err := asked(ctx, s, "ai:stray-check:"+a.ID, f, func() (verdict, error) {
			p, err := s.W.AI.Stray(ctx, f)
			return verdict{p}, err
		})
		if err != nil {
			if s.stop(ctx, err) {
				return changed, err
			}
			continue
		}
		if !fresh || v.P < coach.Bar("stray") {
			continue
		}
		if err := s.Store.SetSyncState(ctx, load.StrayPrefix+a.ID, "stray"); err != nil {
			return changed, err
		}
		s.Log.Info("sweep: stray entry, left out of load", "activity", a.ID, "name", a.Name, "p", v.P)
		changed = true
	}
	return changed, nil
}

// zwiftLine is a line Zwift writes itself ("🗺️ Route in Watopia", "⚡️ New
// 5sec power best"), with or without the emoji variation selector. The
// route is kept elsewhere (ingest.RouteFromDescription) and power bests are
// computed from the data, so neither is the athlete's word. Whoop writes
// whole descriptions ("7.4 Strain amounts to light exertion today.").
var (
	zwiftLine = regexp.MustCompile(`^\s*(\x{1F5FA}|\x{26A1}|\x{1F3C6}|\x{1F3C5}|\x{1F525}|\x{26F0})`)
	whoopText = regexp.MustCompile(`^\d+(\.\d+)? Strain amounts to `)
)

// Description is the athlete's own words on a source, without the lines
// apps write themselves; "" if nothing is left.
func Description(src store.Source) string {
	var ex struct {
		Description string `json:"description"`
	}
	_ = json.Unmarshal(src.Extra, &ex)
	if whoopText.MatchString(ex.Description) {
		return ""
	}
	var keep []string
	for _, l := range strings.Split(ex.Description, "\n") {
		if l = strings.TrimSpace(l); l != "" && !zwiftLine.MatchString(l) {
			keep = append(keep, l)
		}
	}
	return strings.Join(keep, "\n")
}

// TagsKey keys the tags read from one source's description.
func TagsKey(sourceID string) string { return "ai:tags:" + sourceID }

// tags has the LLM read tags from each description the athlete wrote.
func (s *Sweeper) tags(ctx context.Context) (bool, error) {
	all, err := s.Store.AllSources(ctx)
	if err != nil {
		return false, err
	}
	for _, src := range all {
		d := Description(src)
		if d == "" {
			continue
		}
		key, fp := TagsKey(src.ID), coach.Fingerprint(coach.TagsPrompt(), d)
		var t coach.Tags
		if s.W.Cached(ctx, key, fp, &t) {
			continue
		}
		t, err := s.W.DescribeTags(ctx, d)
		if err != nil {
			if s.stop(ctx, err) {
				return false, fmt.Errorf("tags for %s: %w", src.ID, err)
			}
			t = coach.Tags{} // an unreadable reply: no tags, and don't ask again
		}
		if err := s.W.Keep(ctx, key, fp, t); err != nil {
			return false, err
		}
	}
	return false, nil // tags change nothing load depends on
}

// SourceTags returns the tags kept for a source's description, if any.
func SourceTags(ctx context.Context, w *coach.Writer, src store.Source) (coach.Tags, bool) {
	d := Description(src)
	if d == "" || w == nil {
		return coach.Tags{}, false
	}
	var t coach.Tags
	ok := w.Cached(ctx, TagsKey(src.ID), coach.Fingerprint(coach.TagsPrompt(), d), &t)
	return t, ok && (len(t.Tags) > 0 || t.Note != "")
}
