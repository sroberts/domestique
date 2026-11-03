// Package activity turns raw workout files (FIT, GPX, TCX) into normalized
// activities with per-second streams, and computes the metrics derived from them.
package activity

import (
	"math"
	"time"
)

// Sports Domestique distinguishes.
const (
	SportRide        = "ride"
	SportVirtualRide = "virtual_ride"
	SportRun         = "run"
	SportWalk        = "walk"
	SportHike        = "hike"
	SportSwim        = "swim"
	SportStrength    = "strength"
	SportOther       = "other"
)

// IsRide reports whether sport is any kind of cycling.
func IsRide(sport string) bool { return sport == SportRide || sport == SportVirtualRide }

// Missing marks an absent integer sample in a stream.
const Missing = -1

// Stream is columnar sample data at (nominally) one-second resolution. T is the
// offset in seconds from the activity start. Integer channels use Missing for
// gaps; Lat/Lng use 0 when there is no fix. Every slice has len(T) entries or is nil.
type Stream struct {
	T        []int32   `json:"t"`
	Power    []int32   `json:"power,omitempty"`
	HR       []int32   `json:"hr,omitempty"`
	Cadence  []int32   `json:"cadence,omitempty"`
	Speed    []float32 `json:"speed,omitempty"`    // m/s
	Distance []float32 `json:"distance,omitempty"` // m, cumulative
	Altitude []float32 `json:"altitude,omitempty"` // m
	Lat      []float64 `json:"lat,omitempty"`
	Lng      []float64 `json:"lng,omitempty"`
}

// Len is the number of samples.
func (s *Stream) Len() int { return len(s.T) }

// Has reports whether a channel carries any real data.
func hasInt(v []int32) bool {
	for _, x := range v {
		if x > 0 {
			return true
		}
	}
	return false
}

func (s *Stream) HasPower() bool   { return hasInt(s.Power) }
func (s *Stream) HasHR() bool      { return hasInt(s.HR) }
func (s *Stream) HasCadence() bool { return hasInt(s.Cadence) }
func (s *Stream) HasGPS() bool {
	for i := range s.Lat {
		if s.Lat[i] != 0 || s.Lng[i] != 0 {
			return true
		}
	}
	return false
}

// Parsed is one activity read from a file.
type Parsed struct {
	Sport        string
	Name         string // from the file, when it carries one
	Start        time.Time
	ElapsedS     int
	MovingS      int
	DistanceM    float64
	Manufacturer string // "wahoo", "zwift", "garmin", "strava", ... or ""
	Device       string
	Format       string // "fit", "gpx", "tcx"
	Stream       Stream
}

// finish fills totals the file didn't state, from the stream.
func (p *Parsed) finish() {
	s := &p.Stream
	n := s.Len()
	if n == 0 {
		return
	}
	if p.ElapsedS == 0 {
		p.ElapsedS = int(s.T[n-1])
	}
	if p.DistanceM == 0 && len(s.Distance) == n {
		for i := n - 1; i >= 0; i-- {
			if s.Distance[i] > 0 {
				p.DistanceM = float64(s.Distance[i])
				break
			}
		}
	}
	if p.DistanceM == 0 && s.HasGPS() {
		// Integrate GPS when the file has no distance channel (most GPX).
		s.Distance = make([]float32, n)
		var d float64
		pi := -1
		for i := 0; i < n; i++ {
			if s.Lat[i] == 0 && s.Lng[i] == 0 {
				s.Distance[i] = float32(d)
				continue
			}
			if pi >= 0 {
				d += haversine(s.Lat[pi], s.Lng[pi], s.Lat[i], s.Lng[i])
			}
			pi = i
			s.Distance[i] = float32(d)
		}
		p.DistanceM = d
	}
	if p.MovingS == 0 {
		p.MovingS = movingSeconds(s)
	}
}

// movingSeconds counts time with recorded movement or effort, ignoring gaps
// longer than 10 seconds (auto-pause, a café stop).
func movingSeconds(s *Stream) int {
	n := s.Len()
	moving := 0
	for i := 1; i < n; i++ {
		dt := int(s.T[i] - s.T[i-1])
		if dt <= 0 || dt > 10 {
			continue
		}
		active := false
		if len(s.Speed) == n && s.Speed[i] > 0.8 {
			active = true
		} else if len(s.Power) == n && s.Power[i] > 0 {
			active = true
		} else if len(s.Cadence) == n && s.Cadence[i] > 0 {
			active = true
		} else if len(s.Distance) == n && s.Distance[i]-s.Distance[i-1] > 0.8*float32(dt) {
			active = true
		} else if len(s.Speed) != n && len(s.Distance) != n && len(s.Power) != n {
			active = true // HR-only recordings: count the time
		}
		if active {
			moving += dt
		}
	}
	return moving
}

func haversine(lat1, lng1, lat2, lng2 float64) float64 {
	const r = 6371000.0
	toRad := math.Pi / 180
	dLat := (lat2 - lat1) * toRad
	dLng := (lng2 - lng1) * toRad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*toRad)*math.Cos(lat2*toRad)*math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * r * math.Asin(math.Min(1, math.Sqrt(a)))
}

// builder accumulates samples for any parser.
type builder struct {
	start    time.Time
	s        Stream
	any      struct{ power, hr, cad, speed, dist, alt, gps bool }
	lastT    int32
	hasPoint bool
}

type sample struct {
	t                time.Time
	power, hr, cad   int32
	speed, dist, alt float64 // NaN when absent
	lat, lng         float64 // 0 when absent
}

func newSample(t time.Time) sample {
	return sample{t: t, power: Missing, hr: Missing, cad: Missing, speed: math.NaN(), dist: math.NaN(), alt: math.NaN()}
}

func (b *builder) add(x sample) {
	if x.t.IsZero() {
		return
	}
	if !b.hasPoint {
		b.start = x.t
		b.hasPoint = true
	}
	off := int32(x.t.Sub(b.start).Seconds())
	if off < 0 || (b.s.Len() > 0 && off <= b.lastT) {
		return // out of order or duplicate second
	}
	b.lastT = off
	b.s.T = append(b.s.T, off)
	b.s.Power = append(b.s.Power, x.power)
	b.s.HR = append(b.s.HR, x.hr)
	b.s.Cadence = append(b.s.Cadence, x.cad)
	b.s.Speed = append(b.s.Speed, f32(x.speed))
	b.s.Distance = append(b.s.Distance, f32(x.dist))
	b.s.Altitude = append(b.s.Altitude, f32(x.alt))
	b.s.Lat = append(b.s.Lat, x.lat)
	b.s.Lng = append(b.s.Lng, x.lng)
	b.any.power = b.any.power || x.power > 0
	b.any.hr = b.any.hr || x.hr > 0
	b.any.cad = b.any.cad || x.cad > 0
	b.any.speed = b.any.speed || !math.IsNaN(x.speed)
	b.any.dist = b.any.dist || !math.IsNaN(x.dist)
	b.any.alt = b.any.alt || !math.IsNaN(x.alt)
	b.any.gps = b.any.gps || x.lat != 0 || x.lng != 0
}

func f32(v float64) float32 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return -1
	}
	return float32(v)
}

// stream drops channels that carry nothing and forward-fills cumulative distance.
func (b *builder) stream() Stream {
	s := b.s
	if !b.any.power {
		s.Power = nil
	}
	if !b.any.hr {
		s.HR = nil
	}
	if !b.any.cad {
		s.Cadence = nil
	}
	if !b.any.speed {
		s.Speed = nil
	}
	if !b.any.alt {
		s.Altitude = nil
	} else {
		fillFloat(s.Altitude)
	}
	if !b.any.dist {
		s.Distance = nil
	} else {
		fillFloat(s.Distance)
	}
	if !b.any.gps {
		s.Lat, s.Lng = nil, nil
	}
	if s.Speed != nil {
		for i, v := range s.Speed {
			if v < 0 {
				s.Speed[i] = 0
			}
		}
	}
	return s
}

// fillFloat replaces -1 gaps with the previous value (or the next, at the start).
func fillFloat(v []float32) {
	last := float32(-1)
	for i := range v {
		if v[i] < 0 && v[i] != -1 {
			continue // real negative altitude (below sea level)
		}
		if v[i] == -1 {
			v[i] = last
		} else {
			last = v[i]
		}
	}
	first := float32(0)
	for _, x := range v {
		if x != -1 {
			first = x
			break
		}
	}
	for i := range v {
		if v[i] != -1 {
			break
		}
		v[i] = first
	}
}
