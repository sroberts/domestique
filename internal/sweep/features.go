package sweep

import (
	"encoding/json"
	"math"
	"sort"
	"time"

	"domestique/internal/activity"
	"domestique/internal/coach"
	"domestique/internal/store"
)

// Plain numbers worked out from a recording's streams, for System 1 to judge
// (see quality.go). Each sample stands for the time to the next one, capped
// at 5 s, so pauses don't count as recorded time.

// step is the seconds sample i stands for.
func step(t []int32, i int) int {
	if i+1 >= len(t) {
		return 1
	}
	return int(min(max(t[i+1]-t[i], 0), 5))
}

// recorded is a stream's recorded time in seconds, leaving out pauses
// (an auto-paused recording has no samples while stopped).
func recorded(t []int32) int {
	n := 0
	for i := range t {
		n += step(t, i)
	}
	return n
}

func share(part, whole int) float64 {
	if whole <= 0 {
		return 0
	}
	return math.Round(float64(part)/float64(whole)*100) / 100
}

// gaps counts runs of samples with no reading (valid false) lasting 5 s or
// more, between the first reading and the last. A pause in the recording
// has no samples at all, so it isn't a gap.
func gaps(t []int32, valid func(int) bool) int {
	n, last, from := 0, -1, -1
	for i := range t {
		if !valid(i) {
			if from < 0 {
				from = i
			}
			continue
		}
		if last >= 0 && from >= 0 && t[i]-t[from] >= 5 {
			n++
		}
		from, last = -1, i
	}
	return n
}

// flat is the longest run of one unchanged reading, in seconds.
func flat(t []int32, v []int32, ok func(int32) bool) int {
	best, from := 0, -1
	for i := range v {
		if from >= 0 && v[i] == v[from] && ok(v[i]) {
			best = max(best, int(t[i]-t[from]))
			continue
		}
		from = i
		if !ok(v[i]) {
			from = -1
		}
	}
	return best
}

// recordingFacts works out a source's numbers. Channels it doesn't have are nil.
func recordingFacts(src store.Source, sport string, st *activity.Stream, maxHR, ftp int) coach.RecordingFacts {
	f := coach.RecordingFacts{Sport: sport, Source: src.Source, Device: src.Device,
		Minutes: recorded(st.T) / 60, AthleteMaxHR: maxHR, FTP: ftp}
	total := recorded(st.T)
	cad := func(i int) int32 {
		if len(st.Cadence) == len(st.T) {
			return st.Cadence[i]
		}
		return activity.Missing
	}
	if st.HasHR() && len(st.HR) == len(st.T) {
		h := &coach.HRFacts{Cadence: st.HasCadence()}
		var covered, sum, n, zeros, readings, pedal, pedalLocked, cadOK int
		first := -1
		var vals []int
		for i, v := range st.HR {
			if v >= 0 {
				readings++
			}
			if v == 0 {
				zeros++
			}
			if c := cad(i); c >= 0 {
				cadOK++
				if c > 40 {
					pedal++
					if v > 0 && abs32(v-c) <= 2 {
						pedalLocked++
					}
				}
			}
			if v <= 0 {
				continue
			}
			if first < 0 {
				first = i
			}
			d := step(st.T, i)
			covered += d
			sum += int(v)
			n++
			vals = append(vals, int(v))
			h.Max = max(h.Max, int(v))
			if int(v) > maxHR+5 {
				h.OverMaxS += d
			}
		}
		if n == 0 {
			return f
		}
		h.Coverage = share(covered, total)
		h.Avg = sum / n
		h.Dropouts = gaps(st.T, func(i int) bool { return st.HR[i] > 0 })
		h.LongestFlatS = flat(st.T, st.HR, func(v int32) bool { return v > 0 })
		if first > 0 {
			h.FirstReadingS = int(st.T[first] - st.T[0])
		}
		prev := -1
		for i, v := range st.HR {
			if v <= 0 {
				continue
			}
			if prev >= 0 && st.T[i]-st.T[prev] <= 2 && abs32(v-st.HR[prev]) >= 25 {
				h.Jumps++
			}
			prev = i
		}
		sort.Ints(vals)
		h.Spread = vals[len(vals)*9/10] - vals[len(vals)/10]
		if h.Cadence && pedal > 0 {
			h.CadenceLocked = share(pedalLocked, pedal)
			h.Pedalling = share(pedal, cadOK)
		}
		h.Zeros = share(zeros, readings)
		f.HR = h
	}
	if st.HasPower() && len(st.Power) == len(st.T) {
		p := &coach.PowerFacts{}
		var covered, sum, n, pedal, zeroPedal, run, from int
		from = -1
		for i, v := range st.Power {
			if v < 0 {
				continue
			}
			covered += step(st.T, i)
			sum += int(v)
			n++
			p.Max = max(p.Max, int(v))
			if v > 2000 {
				p.Over2000++
			}
			if c := cad(i); c > 50 {
				pedal++
				if v == 0 {
					zeroPedal++
					if from < 0 {
						from = i
					}
					run = int(st.T[i] - st.T[from])
					p.LongestZeroPedal = max(p.LongestZeroPedal, run)
					continue
				}
			}
			from = -1
		}
		if n == 0 {
			return f
		}
		p.Coverage = share(covered, total)
		p.Avg = sum / n
		p.Dropouts = gaps(st.T, func(i int) bool { return st.Power[i] >= 0 })
		p.LongestFlatS = flat(st.T, st.Power, func(v int32) bool { return v > 0 })
		if pedal > 0 {
			p.ZeroPedalling = share(zeroPedal, pedal)
		}
		f.Power = p
	}
	return f
}

func abs32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

// hrReason and powerReason say in a few words what looked wrong, for the
// notice on the activity page.
func hrReason(h *coach.HRFacts) string {
	switch {
	case h.CadenceLocked >= 0.2:
		return "it followed cadence"
	case h.OverMaxS > 0:
		return "readings over your max"
	case h.Dropouts >= 3 || h.Coverage < 0.85 || h.Zeros >= 0.05:
		return "strap dropouts"
	case h.LongestFlatS >= 120:
		return "it stuck on one reading"
	case h.Jumps >= 10:
		return "sudden jumps"
	}
	return "it didn't look like heart rate"
}

func powerReason(p *coach.PowerFacts) string {
	switch {
	case p.ZeroPedalling >= 0.1 || p.LongestZeroPedal >= 30:
		return "zeros while pedalling"
	case p.Over2000 > 0:
		return "impossible spikes"
	case p.Dropouts >= 3 || p.Coverage < 0.85:
		return "meter dropouts"
	case p.LongestFlatS >= 120:
		return "it stuck on one reading"
	}
	return "it didn't look like real power"
}

var powerZoneNames = []string{"z1_recovery", "z2_endurance", "z3_tempo", "z4_threshold", "z5_vo2max", "z6_anaerobic", "z7_sprint"}
var hrZoneNames = []string{"z1_under_60pct", "z2_60_70pct", "z3_70_80pct", "z4_80_90pct", "z5_over_90pct"}

// rideFacts works out the numbers a ride is classified by.
func rideFacts(a store.Activity, st *activity.Stream, maxHR int, loc *time.Location, notes string) coach.RideFacts {
	moving := a.MovingS
	if moving <= 0 {
		moving = a.ElapsedS
	}
	f := coach.RideFacts{Name: a.Name, Start: time.UnixMilli(a.StartAt).In(loc).Format("Mon 15:04"),
		Minutes: int(math.Round(float64(moving) / 60)), Indoor: a.Sport == activity.SportVirtualRide, Notes: notes}
	if a.DistanceM > 0 {
		km := math.Round(a.DistanceM/100) / 10
		f.KM = &km
	}
	var m activity.Metrics
	_ = json.Unmarshal(a.Metrics, &m)
	if a.Intensity > 0 {
		v := math.Round(a.Intensity*100) / 100
		f.IF = &v
	}
	if m.NP > 0 && m.AvgPower > 0 {
		v := math.Round(float64(m.NP)/float64(m.AvgPower)*100) / 100
		f.VI = &v
	}
	if a.FTP > 0 && st != nil && st.HasPower() {
		z := activity.PowerZoneSeconds(st, a.FTP)
		t := 0
		for _, s := range z {
			t += s
		}
		if t > 0 {
			f.PowerZones = map[string]float64{}
			for i, s := range z {
				f.PowerZones[powerZoneNames[i]] = share(s, t)
			}
		}
	}
	if len(m.HRHist) > 0 && maxHR > 0 {
		z := activity.HRZoneSeconds(m.HRHist, maxHR)
		z[1] += z[0] // under 50% and 50-60% are both zone 1 here
		t := 0
		for _, s := range z[1:] {
			t += s
		}
		if t > 0 {
			f.HRZones = map[string]float64{}
			for i, s := range z[1:] {
				f.HRZones[hrZoneNames[i]] = share(s, t)
			}
		}
	}
	if m.AvgHR > 0 && maxHR > 0 {
		v := int(math.Round(float64(m.AvgHR) * 100 / float64(maxHR)))
		f.AvgHRPct = &v
	}
	if st != nil {
		if n, ok := hardEfforts(st, a.FTP, maxHR); ok {
			f.HardEfforts = &n
		}
		if !f.Indoor && len(st.Speed) == len(st.T) && len(st.Speed) > 0 {
			n := stops(st)
			f.Stops = &n
		}
	}
	return f
}

// hardEfforts counts efforts over threshold: 10 s average power of 105%
// FTP or more for 30 s or more, or with no power, heart rate of 90% of max
// or more for 60 s or more. Dips under 5 s don't end an effort.
func hardEfforts(st *activity.Stream, ftp, maxHR int) (int, bool) {
	var v []int32
	var lim float64
	minDur := int32(30)
	switch {
	case ftp > 0 && st.HasPower() && len(st.Power) == len(st.T):
		v, lim = smooth(st.Power, 10), 1.05*float64(ftp)
	case maxHR > 0 && st.HasHR() && len(st.HR) == len(st.T):
		v, lim, minDur = st.HR, 0.9*float64(maxHR), 60
	default:
		return 0, false
	}
	n := 0
	from, lastHigh := int32(-1), int32(-1)
	for i, x := range v {
		t := st.T[i]
		if x >= 0 && float64(x) >= lim {
			if from < 0 || t-lastHigh > 5 {
				if from >= 0 && lastHigh-from >= minDur {
					n++
				}
				from = t
			}
			lastHigh = t
		}
	}
	if from >= 0 && lastHigh-from >= minDur {
		n++
	}
	return n, true
}

// smooth is a trailing mean over w samples, skipping missing ones.
func smooth(v []int32, w int) []int32 {
	out := make([]int32, len(v))
	var sum, n int
	for i := range v {
		if v[i] >= 0 {
			sum += int(v[i])
			n++
		}
		if j := i - w; j >= 0 && v[j] >= 0 {
			sum -= int(v[j])
			n--
		}
		if n > 0 {
			out[i] = int32(sum / n)
		} else {
			out[i] = activity.Missing
		}
	}
	return out
}

// stops counts times the rider stood still for 15 s or more, after the
// start and before the finish.
func stops(st *activity.Stream) int {
	n := 0
	from := int32(-1)
	moved := false
	for i, sp := range st.Speed {
		t := st.T[i]
		if math.IsNaN(float64(sp)) {
			continue
		}
		if sp < 0.5 {
			if from < 0 && moved {
				from = t
			}
			continue
		}
		if from >= 0 && t-from >= 15 {
			n++
		}
		from = -1
		moved = true
	}
	return n
}
