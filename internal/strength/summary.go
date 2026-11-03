// Package strength slices a heart rate stream by logged set and rest period.
package strength

import (
	"math"
	"sort"

	"domestique/internal/store"
)

// Zones holds the lower bound (bpm) of zones 1..5.
type Zones [5]int

// ZonesFor derives zones from max heart rate as 50/60/70/80/90 percent.
func ZonesFor(maxHR int) Zones {
	var z Zones
	for i, pct := range []float64{0.5, 0.6, 0.7, 0.8, 0.9} {
		z[i] = int(math.Round(float64(maxHR) * pct))
	}
	return z
}

// Of returns the zone (1..5) for bpm, or 0 below zone 1.
func (z Zones) Of(bpm int) int {
	for i := 4; i >= 0; i-- {
		if bpm >= z[i] {
			return i + 1
		}
	}
	return 0
}

// SetStat is the heart rate story of one set and the rest that followed it.
type SetStat struct {
	SetID string `json:"set_id"`
	HasHR bool   `json:"has_hr"`
	// Peak and average while lifting. Peak looks 15s past the end of the set,
	// because heart rate lags effort.
	PeakBPM int `json:"peak_bpm"`
	AvgBPM  int `json:"avg_bpm"`
	Zone    int `json:"zone"`
	// RecoveryDrop is peak minus heart rate at the end of the recovery window:
	// 60s after the set, or the start of the next set if that came sooner.
	RecoveryDrop    int `json:"recovery_drop"`
	RecoveryWindowS int `json:"recovery_window_s"`
	RestS           int `json:"rest_s"` // actual rest taken before the next set; 0 for the last
}

// Summary is everything the Session Summary screen shows.
type Summary struct {
	Session  store.Session    `json:"session"`
	Sets     []store.Set      `json:"sets"`
	HR       []store.HRSample `json:"hr"`
	HRStatus string           `json:"hr_status"` // "ready" | "pending" | "none"
	MaxHR    int              `json:"max_hr"`
	Zones    Zones            `json:"zones"`

	DurationS   int       `json:"duration_s"`
	VolumeKg    float64   `json:"volume_kg"`
	TotalReps   int       `json:"total_reps"`
	PeakBPM     int       `json:"peak_bpm"`
	AvgBPM      int       `json:"avg_bpm"`
	ZoneSeconds [6]int    `json:"zone_seconds"` // index 0 = below zone 1
	SetStats    []SetStat `json:"set_stats"`
	Progress    Progress  `json:"progress"`
}

// HRPadding is how much stream to include either side of the session.
const HRPadding = 3 * 60 * 1000

// Build computes a summary. hr must be sorted by time.
func Build(sess store.Session, sets []store.Set, hr []store.HRSample, maxHR int) Summary {
	sort.Slice(sets, func(i, j int) bool { return sets[i].StartedAt < sets[j].StartedAt })
	zones := ZonesFor(maxHR)
	sum := Summary{
		Session: sess, Sets: sets, HR: hr, MaxHR: maxHR, Zones: zones,
		HRStatus: "pending", SetStats: make([]SetStat, 0, len(sets)),
		Progress: ProgressOf(sess.Plan, sets),
	}
	end := sess.StartedAt
	if sess.FinishedAt != nil {
		end = *sess.FinishedAt
	} else if n := len(sets); n > 0 {
		end = sets[n-1].EndedAt
	}
	sum.DurationS = int((end - sess.StartedAt) / 1000)

	for _, s := range sets {
		sum.TotalReps += s.Reps
		sum.VolumeKg += float64(s.Reps) * s.WeightKg
	}

	// Session-wide stats over the session window only.
	var total, n int
	for i, h := range hr {
		if h.T < sess.StartedAt || h.T > end {
			continue
		}
		total += h.BPM
		n++
		if h.BPM > sum.PeakBPM {
			sum.PeakBPM = h.BPM
		}
		sum.ZoneSeconds[zones.Of(h.BPM)] += sampleSeconds(hr, i)
	}
	if n > 0 {
		sum.AvgBPM = int(math.Round(float64(total) / float64(n)))
		sum.HRStatus = "ready"
	}

	for i, s := range sets {
		st := SetStat{SetID: s.ID}
		var nextStart int64
		if i+1 < len(sets) {
			nextStart = sets[i+1].StartedAt
			st.RestS = int((nextStart - s.EndedAt) / 1000)
		}
		peakEnd := s.EndedAt + 15_000
		if nextStart != 0 && nextStart < peakEnd {
			peakEnd = nextStart
		}
		var tot, cnt, peak int
		var peakT int64
		for _, h := range window(hr, s.StartedAt, peakEnd) {
			if h.T <= s.EndedAt {
				tot += h.BPM
				cnt++
			}
			if h.BPM > peak {
				peak, peakT = h.BPM, h.T
			}
		}
		if cnt > 0 && peak > 0 {
			st.HasHR = true
			st.PeakBPM = peak
			st.AvgBPM = int(math.Round(float64(tot) / float64(cnt)))
			st.Zone = zones.Of(peak)
			recEnd := s.EndedAt + 60_000
			if nextStart != 0 && nextStart < recEnd {
				recEnd = nextStart
			}
			if recEnd > peakT {
				if h, ok := at(hr, recEnd); ok {
					st.RecoveryDrop = peak - h.BPM
					st.RecoveryWindowS = int((recEnd - s.EndedAt) / 1000)
				}
			}
		}
		sum.SetStats = append(sum.SetStats, st)
	}
	return sum
}

// window returns samples with from <= t <= to.
func window(hr []store.HRSample, from, to int64) []store.HRSample {
	lo := sort.Search(len(hr), func(i int) bool { return hr[i].T >= from })
	hi := sort.Search(len(hr), func(i int) bool { return hr[i].T > to })
	return hr[lo:hi]
}

// at returns the sample nearest t, if one exists within 10 seconds.
func at(hr []store.HRSample, t int64) (store.HRSample, bool) {
	i := sort.Search(len(hr), func(i int) bool { return hr[i].T >= t })
	best, ok := store.HRSample{}, false
	var bestD int64 = 10_001
	for _, j := range []int{i - 1, i} {
		if j < 0 || j >= len(hr) {
			continue
		}
		d := hr[j].T - t
		if d < 0 {
			d = -d
		}
		if d < bestD {
			best, bestD, ok = hr[j], d, true
		}
	}
	return best, ok
}

// sampleSeconds is the time a sample represents: the gap to the next sample,
// capped so a dropout doesn't count as minutes in one zone.
func sampleSeconds(hr []store.HRSample, i int) int {
	if i+1 >= len(hr) {
		return 1
	}
	d := int((hr[i+1].T - hr[i].T) / 1000)
	if d < 1 {
		return 1
	}
	if d > 5 {
		return 5
	}
	return d
}
