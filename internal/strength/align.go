package strength

import (
	"math"
	"sort"

	"domestique/internal/store"
)

// Clock drift. Set timestamps come from the phone; heart rate comes from a
// strap or watch with its own clock, relayed through Apple Health or a file.
// Two corrections line them up:
//
//  1. The phone measures its offset from the Mac Mini while it syncs
//     (Session.ClockOffsetMs), which fixes the phone's clock.
//  2. What's left, the heart rate source's own error, is estimated from the
//     data: heart rate climbs during every set and falls during rest, so the
//     shift that best lines those climbs up with the logged sets is the
//     source's offset. A manual shift (Session.HRShiftMs) overrides it.

// MaxShift bounds the automatic search either way.
const MaxShift = 120_000

// Alignment says how heart rate was lined up with a session's sets.
type Alignment struct {
	ClockOffsetMs int64   `json:"clock_offset_ms"` // phone clock correction
	HRShiftMs     int64   `json:"hr_shift_ms"`     // heart rate source correction
	Source        string  `json:"source"`          // "auto" | "manual" | "none"
	Confidence    float64 `json:"confidence"`      // auto only: how far the best fit beat any rival shift (correlation)
}

// EstimateShift finds how far heart rate runs behind the sets: hr is in the
// phone's time base (already corrected by the clock offset), sets in device
// time. It predicts the heart rate curve the logged sets should produce (a
// rise towards a working rate during each set, a slower fall in rest), then
// slides the recorded curve against it and keeps the shift where the two
// correlate best. It returns ok only when that answer is clear: at least
// three real sets, a strong fit, and no rival shift nearly as good.
func EstimateShift(sets []store.Set, hr []store.HRSample) (shift int64, confidence float64, ok bool) {
	real := Counted(sets)
	if len(real) < 3 || len(hr) < 30 {
		return 0, 0, false
	}
	sort.Slice(real, func(i, j int) bool { return real[i].StartedAt < real[j].StartedAt })

	// Predicted curve, 1 Hz, over the session with a minute either side.
	from := real[0].StartedAt - 60_000
	to := real[len(real)-1].EndedAt + 90_000
	n := int((to - from) / 1000)
	pred := make([]float64, n)
	level, k := 0.0, 0
	for i := range pred {
		t := from + int64(i)*1000
		for k < len(real) && real[k].EndedAt < t {
			k++
		}
		if k < len(real) && t >= real[k].StartedAt && t <= real[k].EndedAt {
			level += (1 - level) / riseTau
		} else {
			level -= level / fallTau
		}
		pred[i] = level
	}

	// Recorded curve at 1 Hz over the same span plus the search margin.
	rec := resample(hr, from-MaxShift, to+MaxShift)
	if rec == nil {
		return 0, 0, false
	}
	corr := func(delta int64) (float64, bool) {
		off := int((delta + MaxShift) / 1000)
		var sx, sy, sxx, syy, sxy float64
		var m int
		for i, p := range pred {
			y := rec[i+off]
			if math.IsNaN(y) {
				continue
			}
			sx, sy, sxx, syy, sxy = sx+p, sy+y, sxx+p*p, syy+y*y, sxy+p*y
			m++
		}
		if m < n*2/3 {
			return 0, false // too little heart rate under the session at this shift
		}
		fm := float64(m)
		cov := sxy - sx*sy/fm
		vx, vy := sxx-sx*sx/fm, syy-sy*sy/fm
		if vx <= 0 || vy <= 0 {
			return 0, false
		}
		return cov / math.Sqrt(vx*vy), true
	}

	type cand struct {
		delta int64
		r     float64
	}
	var all []cand
	best := cand{r: math.Inf(-1)}
	for delta := int64(-MaxShift); delta <= MaxShift; delta += 1000 {
		r, ok := corr(delta)
		if !ok {
			continue
		}
		all = append(all, cand{delta, r})
		if r > best.r {
			best = cand{delta, r}
		}
	}
	if len(all) < 60 || best.r < minFit {
		return 0, 0, false
	}
	// The best rival at least 20 s away: a near-tie means the sets' rhythm
	// fits more than one shift, and guessing would be worse than not.
	rival := math.Inf(-1)
	for _, c := range all {
		if d := c.delta - best.delta; (d > 20_000 || d < -20_000) && c.r > rival {
			rival = c.r
		}
	}
	margin := best.r - rival
	if margin < minMargin {
		return 0, margin, false
	}
	// Within 5 s of no shift: leave it alone. Heart rate arrives every
	// 5 s or so and the predicted curve is only a model, so smaller shifts
	// are below what the data can tell apart.
	if best.delta >= -deadband && best.delta <= deadband {
		return 0, margin, true
	}
	return best.delta, margin, true
}

// The predicted curve's time constants, in seconds: heart rate climbs within
// half a minute of starting a set and takes a minute or more to settle.
const (
	riseTau = 20.0
	fallTau = 45.0
	// minFit is the least correlation that counts as a fit; minMargin how far
	// it must beat any shift 20 s or more away.
	minFit    = 0.6
	minMargin = 0.05
	deadband  = 5_000
)

// resample linearly interpolates heart rate at 1 Hz over [from, to); gaps
// longer than 15 s are NaN rather than invented.
func resample(hr []store.HRSample, from, to int64) []float64 {
	n := int((to - from) / 1000)
	if n <= 0 {
		return nil
	}
	out := make([]float64, n)
	j := 0
	any := false
	for i := range out {
		t := from + int64(i)*1000
		for j+1 < len(hr) && hr[j+1].T <= t {
			j++
		}
		out[i] = math.NaN()
		if j+1 >= len(hr) || hr[j].T > t {
			continue
		}
		a, b := hr[j], hr[j+1]
		if b.T-a.T > 15_000 {
			continue
		}
		f := float64(t-a.T) / float64(b.T-a.T)
		out[i] = float64(a.BPM) + f*float64(b.BPM-a.BPM)
		any = true
	}
	if !any {
		return nil
	}
	return out
}

// Align puts heart rate on the phone's time base for a session: the phone's
// clock offset, then the source's shift (manual, else estimated). hr must be
// on true time and sorted; the result is a new slice.
func Align(sess store.Session, sets []store.Set, hr []store.HRSample) ([]store.HRSample, Alignment) {
	al := Alignment{ClockOffsetMs: sess.ClockOffsetMs, Source: "none"}
	dev := make([]store.HRSample, len(hr))
	for i, h := range hr {
		dev[i] = store.HRSample{T: h.T - sess.ClockOffsetMs, BPM: h.BPM}
	}
	switch {
	case sess.HRShiftMs != nil:
		al.HRShiftMs, al.Source = *sess.HRShiftMs, "manual"
	default:
		if shift, z, ok := EstimateShift(sets, dev); ok {
			al.HRShiftMs, al.Source, al.Confidence = shift, "auto", math.Round(z*100)/100
		}
	}
	if al.HRShiftMs != 0 {
		for i := range dev {
			dev[i].T -= al.HRShiftMs
		}
	}
	return dev, al
}
