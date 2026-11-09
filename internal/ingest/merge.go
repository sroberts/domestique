package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"domestique/internal/activity"
	"domestique/internal/store"
)

// Channel precedence (spec: Deduplication). Earlier wins. A Wahoo head unit
// with a chest strap beats a wrist band whenever both recorded heart rate.
var (
	powerOrder = []string{"wahoo", "garmin", "device", "zwift", "upload", "watch", "strava"}
	hrOrder    = []string{"wahoo", "garmin", "device", "zwift", "upload", "watch", "strava", "app", "whoop"}
	gpsOrder   = []string{"wahoo", "garmin", "device", "upload", "watch", "strava", "zwift"}
)

func rank(order []string, kind string) int {
	for i, k := range order {
		if k == kind {
			return i
		}
	}
	return len(order)
}

// Merged is an activity's best-of-all-sources view.
type Merged struct {
	Stream  activity.Stream
	StartAt int64
	// Which source supplied each channel, for the UI's source badges.
	Channels map[string]string `json:"channels"`
}

type loaded struct {
	src    store.Source
	stream *activity.Stream
}

func (in *Ingester) loadSources(ctx context.Context, actID string) ([]loaded, error) {
	srcs, err := in.Store.SourcesFor(ctx, actID)
	if err != nil {
		return nil, err
	}
	out := make([]loaded, 0, len(srcs))
	for _, s := range srcs {
		l := loaded{src: s}
		var st activity.Stream
		if err := in.Store.GetStream(ctx, s.ID, &st); err == nil {
			l.stream = &st
		} else if !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
		out = append(out, l)
	}
	return out, nil
}

func pick(ls []loaded, order []string, has func(*activity.Stream) bool) *loaded {
	var best *loaded
	for i := range ls {
		l := &ls[i]
		if l.stream == nil || !has(l.stream) {
			continue
		}
		if best == nil || rank(order, l.src.Source) < rank(order, best.src.Source) {
			best = l
		}
	}
	return best
}

// MergedStream builds the canonical stream for an activity.
func (in *Ingester) MergedStream(ctx context.Context, actID string) (*Merged, []store.Source, error) {
	ls, err := in.loadSources(ctx, actID)
	if err != nil {
		return nil, nil, err
	}
	srcs := make([]store.Source, len(ls))
	for i, l := range ls {
		srcs[i] = l.src
	}
	m := merge(ls)
	return m, srcs, nil
}

func merge(ls []loaded) *Merged {
	m := &Merged{Channels: map[string]string{}}
	pw := pick(ls, powerOrder, (*activity.Stream).HasPower)
	hr := pick(ls, hrOrder, (*activity.Stream).HasHR)
	gps := pick(ls, gpsOrder, (*activity.Stream).HasGPS)
	cad := pick(ls, powerOrder, (*activity.Stream).HasCadence)

	base := pw
	for _, c := range []*loaded{hr, gps} {
		if base == nil {
			base = c
		}
	}
	if base == nil {
		for i := range ls {
			if ls[i].stream != nil && ls[i].stream.Len() > 0 {
				base = &ls[i]
				break
			}
		}
	}
	if base == nil {
		if len(ls) > 0 {
			m.StartAt = ls[0].src.StartAt
		}
		return m
	}
	m.StartAt = base.src.StartAt
	s := *base.stream // copy header; channels below are replaced as needed
	n := s.Len()

	// align copies another source's channel onto the base timeline.
	align := func(from *loaded, get func(*activity.Stream) []int32) []int32 {
		out := make([]int32, n)
		for i := range out {
			out[i] = activity.Missing
		}
		idx := map[int64]int32{}
		fs := from.stream
		vals := get(fs)
		for i, t := range fs.T {
			idx[from.src.StartAt/1000+int64(t)] = vals[i]
		}
		for i, t := range s.T {
			if v, ok := idx[base.src.StartAt/1000+int64(t)]; ok {
				out[i] = v
			}
		}
		return out
	}
	alignF := func(from *loaded, get func(*activity.Stream) []float32) []float32 {
		out := make([]float32, n)
		idx := map[int64]float32{}
		fs := from.stream
		vals := get(fs)
		for i, t := range fs.T {
			idx[from.src.StartAt/1000+int64(t)] = vals[i]
		}
		last := float32(0)
		for i, t := range s.T {
			if v, ok := idx[base.src.StartAt/1000+int64(t)]; ok {
				last = v
			}
			out[i] = last
		}
		return out
	}

	if pw != nil {
		m.Channels["power"] = pw.src.Source
		if pw != base {
			s.Power = align(pw, func(x *activity.Stream) []int32 { return x.Power })
		}
	} else {
		s.Power = nil
	}
	if hr != nil {
		m.Channels["hr"] = hr.src.Source
		if hr != base {
			s.HR = align(hr, func(x *activity.Stream) []int32 { return x.HR })
		}
	} else {
		s.HR = nil
	}
	if cad != nil {
		m.Channels["cadence"] = cad.src.Source
		if cad != base {
			s.Cadence = align(cad, func(x *activity.Stream) []int32 { return x.Cadence })
		}
	} else {
		s.Cadence = nil
	}
	if gps != nil {
		m.Channels["gps"] = gps.src.Source
		if gps != base {
			lat := map[int64][2]float64{}
			for i, t := range gps.stream.T {
				lat[gps.src.StartAt/1000+int64(t)] = [2]float64{gps.stream.Lat[i], gps.stream.Lng[i]}
			}
			s.Lat, s.Lng = make([]float64, n), make([]float64, n)
			for i, t := range s.T {
				if p, ok := lat[base.src.StartAt/1000+int64(t)]; ok {
					s.Lat[i], s.Lng[i] = p[0], p[1]
				}
			}
			if gps.stream.Altitude != nil {
				s.Altitude = alignF(gps, func(x *activity.Stream) []float32 { return x.Altitude })
			}
			if gps.stream.Distance != nil {
				s.Distance = alignF(gps, func(x *activity.Stream) []float32 { return x.Distance })
			}
		}
	}
	m.Stream = s
	return m
}

// rideSport decides whether a ride was indoors.
//
// Whoop can't know: it never records GPS, so its rides read as indoor only
// for want of a track. It decides only when it's the sole recording; next to
// a bike computer (Wahoo, which carries the GPS) or Zwift, it has no say.
//
// Among the rest, a recording that says outdoor has evidence for it: a GPS
// track, an outdoor sub-sport, a climb on the altimeter, or the athlete's
// own label. One without GPS that says indoor may only be guessing from the
// missing track, so it can't overrule that. One that has a track and still
// says indoor (Zwift, a head unit set to indoor) is sure, and wins.
func rideSport(ls []loaded) string {
	var sure, guess, outdoor bool
	others := slices.ContainsFunc(ls, func(l loaded) bool {
		return l.src.Source != "whoop" && activity.IsRide(l.src.Sport)
	})
	for _, l := range ls {
		if others && l.src.Source == "whoop" {
			continue
		}
		switch s := l.src; {
		case s.Sport == activity.SportVirtualRide && s.HasGPS:
			sure = true
		case s.Sport == activity.SportVirtualRide:
			guess = true
		case s.Sport == activity.SportRide:
			outdoor = true
		}
	}
	if sure || guess && !outdoor {
		return activity.SportVirtualRide
	}
	return activity.SportRide
}

// Rebuild recomputes an activity from all of its sources. FTP-dependent load
// is left to RecalcLoad, which runs once per import batch.
func (in *Ingester) Rebuild(ctx context.Context, actID string) error {
	ls, err := in.loadSources(ctx, actID)
	if err != nil {
		return err
	}
	if len(ls) == 0 {
		return in.Store.DeleteActivityIfEmpty(ctx, actID)
	}
	prev, err := in.Store.GetActivity(ctx, actID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	m := merge(ls)
	metrics := activity.Compute(&m.Stream)

	a := prev
	a.ID = actID
	a.UpdatedAt = time.Now().UnixMilli()

	// Timing and distance: the merged base, unless a better source has it.
	var base *store.Source
	for i := range ls {
		s := &ls[i].src
		if s.StartAt == m.StartAt && (base == nil || ls[i].stream != nil) {
			base = s
		}
	}
	if base == nil {
		base = &ls[0].src
	}
	a.StartAt = base.StartAt
	a.ElapsedS = base.ElapsedS
	a.MovingS = base.MovingS
	if a.MovingS == 0 {
		a.MovingS = a.ElapsedS
	}

	// Sport: strength if the app logged it; for rides, indoor or outdoor.
	a.Sport = base.Sport
	if activity.IsRide(a.Sport) {
		a.Sport = rideSport(ls)
	}
	var app, zwift, strava, whoop *store.Source
	for i := range ls {
		s := &ls[i].src
		switch s.Source {
		case "app":
			app = s
		case "zwift":
			zwift = s
		case "strava":
			strava = s
		case "whoop":
			whoop = s
		}
	}
	if app != nil {
		a.Sport = activity.SportStrength
		a.StartAt, a.ElapsedS, a.MovingS = app.StartAt, app.ElapsedS, app.ElapsedS
		sid := strings.TrimPrefix(app.ID, "app:")
		a.SessionID = &sid
	}

	// Distance: Zwift's virtual distance for virtual rides, else the GPS source.
	a.DistanceM = base.DistanceM
	if a.Sport == activity.SportVirtualRide && zwift != nil && zwift.DistanceM > 0 {
		a.DistanceM = zwift.DistanceM
	} else {
		for _, l := range ls {
			if l.src.Source == m.Channels["gps"] && l.src.DistanceM > 0 {
				a.DistanceM = l.src.DistanceM
				break
			}
		}
	}

	// Name: the athlete's own title (Strava, the app) beats a device default.
	a.Name = ""
	for _, s := range []*store.Source{app, strava} {
		if s != nil && s.Name != "" {
			a.Name = s.Name
			break
		}
	}
	a.Route = ""
	for _, l := range ls {
		var ex map[string]any
		_ = json.Unmarshal(l.src.Extra, &ex)
		if r, ok := ex["route"].(string); ok && r != "" && a.Route == "" {
			a.Route = r
		}
	}
	if a.Name == "" && zwift != nil && zwift.Name != "" {
		a.Name = zwift.Name
	}
	if a.Name == "" {
		for _, l := range ls {
			if l.src.Name != "" {
				a.Name = l.src.Name
				break
			}
		}
	}
	if a.Name == "" {
		loc := in.Loc
		if loc == nil {
			loc = time.Local
		}
		a.Name = DefaultName(a.Sport, time.UnixMilli(a.StartAt).In(loc))
	}

	// Strain is Whoop's.
	a.Strain = nil
	for _, s := range []*store.Source{whoop, app} {
		if s == nil {
			continue
		}
		var ex map[string]any
		_ = json.Unmarshal(s.Extra, &ex)
		if v, ok := ex["strain"].(float64); ok {
			a.Strain = &v
			break
		}
	}

	// Circuit share comes from the app's sets.
	metrics.CircuitShare = 0
	if app != nil {
		var ex struct {
			Share float64 `json:"circuit_share"`
		}
		_ = json.Unmarshal(app.Extra, &ex)
		metrics.CircuitShare = ex.Share
	}

	// With no HR stream anywhere, fall back to Whoop's own summary.
	if metrics.AvgHR == 0 && whoop != nil {
		var ex struct {
			Avg int `json:"avg_hr"`
			Max int `json:"max_hr"`
		}
		_ = json.Unmarshal(whoop.Extra, &ex)
		metrics.AvgHR, metrics.MaxHR = ex.Avg, ex.Max
	}

	b, err := json.Marshal(metrics)
	if err != nil {
		return err
	}
	a.Metrics = b
	if err := in.Store.PutActivity(ctx, a); err != nil {
		return fmt.Errorf("put activity: %w", err)
	}
	return nil
}

// DefaultName is what Strava would call it: "Morning Ride", "Evening Lift".
// t should be in the athlete's zone.
func DefaultName(sport string, t time.Time) string {
	part := "Night"
	switch h := t.Hour(); {
	case h >= 5 && h < 12:
		part = "Morning"
	case h >= 12 && h < 17:
		part = "Afternoon"
	case h >= 17 && h < 21:
		part = "Evening"
	}
	noun := map[string]string{
		activity.SportRide: "Ride", activity.SportVirtualRide: "Indoor Ride", activity.SportRun: "Run",
		activity.SportWalk: "Walk", activity.SportHike: "Hike", activity.SportSwim: "Swim",
		activity.SportStrength: "Lift",
	}[sport]
	if noun == "" {
		noun = "Workout"
	}
	return part + " " + noun
}
