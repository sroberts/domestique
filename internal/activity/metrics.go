package activity

import (
	"math"
	"sort"
)

// CurveDurations are the windows (seconds) of the mean-maximal power curve.
var CurveDurations = []int{1, 5, 10, 15, 30, 60, 120, 180, 300, 480, 600, 1200, 1800, 2700, 3600, 5400, 7200}

// Metrics are the FTP-independent summary of a stream. FTP-dependent numbers
// (TSS, IF, zones) are computed from these on demand, because the athlete's
// FTP changes over time and is re-estimated as data arrives.
type Metrics struct {
	AvgPower   int     `json:"avg_power"`
	NP         int     `json:"np"`
	MaxPower   int     `json:"max_power"`
	KJ         float64 `json:"kj"`
	AvgHR      int     `json:"avg_hr"`
	MaxHR      int     `json:"max_hr"`
	AvgCadence int     `json:"avg_cadence"`
	AscentM    float64 `json:"ascent_m"`
	// PowerCurve[i] is the best mean power for CurveDurations[i] (0 if shorter).
	PowerCurve []int `json:"power_curve"`
	// HRHist[b] is seconds spent at b bpm, for HR-based load at any threshold.
	HRHist []int32 `json:"hr_hist"`
	// PowerSeconds is the recorded time with a power meter.
	PowerSeconds int `json:"power_seconds"`
}

// dense is a 1Hz resampling split into contiguous segments.
type dense struct {
	segs [][]float64
}

// resample converts an irregular channel to 1Hz segments. Gaps up to 10s are
// held at the last value; longer gaps (pauses) split segments.
func resample(t []int32, v []int32) dense {
	var d dense
	var cur []float64
	for i := range t {
		if v[i] < 0 {
			continue
		}
		if i > 0 && len(cur) > 0 {
			gap := int(t[i] - t[i-1])
			if gap > 10 {
				d.segs = append(d.segs, cur)
				cur = nil
			} else {
				for k := 1; k < gap; k++ {
					cur = append(cur, cur[len(cur)-1])
				}
			}
		}
		cur = append(cur, float64(v[i]))
	}
	if len(cur) > 0 {
		d.segs = append(d.segs, cur)
	}
	return d
}

// Compute derives FTP-independent metrics.
func Compute(s *Stream) Metrics {
	var m Metrics
	m.PowerCurve = make([]int, len(CurveDurations))
	if s.Len() == 0 {
		return m
	}
	if s.HasPower() {
		p := resample(s.T, s.Power)
		var sum float64
		var n int
		var fourth float64
		var fourthN int
		for _, seg := range p.segs {
			for _, w := range seg {
				sum += w
				n++
				if int(w) > m.MaxPower && w < 2500 {
					m.MaxPower = int(w)
				}
			}
			// Normalized power: 30s rolling mean, fourth-power average.
			if len(seg) >= 30 {
				var roll float64
				for i, w := range seg {
					roll += w
					if i >= 30 {
						roll -= seg[i-30]
					}
					if i >= 29 {
						r := roll / 30
						fourth += r * r * r * r
						fourthN++
					}
				}
			}
			for di, dur := range CurveDurations {
				if best := bestMean(seg, dur); best > m.PowerCurve[di] {
					m.PowerCurve[di] = best
				}
			}
		}
		if n > 0 {
			m.AvgPower = int(math.Round(sum / float64(n)))
			m.KJ = math.Round(sum/1000*10) / 10
			m.PowerSeconds = n
		}
		if fourthN > 0 {
			m.NP = int(math.Round(math.Pow(fourth/float64(fourthN), 0.25)))
		}
	}
	if s.HasHR() {
		m.HRHist = make([]int32, 241)
		var sum, n int
		for i := range s.T {
			h := s.HR[i]
			if h < 30 || h > 240 {
				continue
			}
			dt := int32(1)
			if i+1 < len(s.T) {
				dt = s.T[i+1] - s.T[i]
				if dt > 10 {
					dt = 1
				}
			}
			m.HRHist[h] += dt
			sum += int(h) * int(dt)
			n += int(dt)
		}
		if n > 0 {
			m.AvgHR = int(math.Round(float64(sum) / float64(n)))
		}
		m.MaxHR = robustMax(s.HR)
	}
	if s.HasCadence() {
		var sum, n int
		for _, c := range s.Cadence {
			if c > 0 && c < 250 { // average excludes coasting, as head units do
				sum += int(c)
				n++
			}
		}
		if n > 0 {
			m.AvgCadence = sum / n
		}
	}
	if len(s.Altitude) == s.Len() {
		m.AscentM = ascent(s.Altitude)
	}
	return m
}

// bestMean is the highest mean over any window of dur samples.
func bestMean(seg []float64, dur int) int {
	if len(seg) < dur {
		return 0
	}
	var w float64
	for i := 0; i < dur; i++ {
		w += seg[i]
	}
	best := w
	for i := dur; i < len(seg); i++ {
		w += seg[i] - seg[i-dur]
		if w > best {
			best = w
		}
	}
	return int(math.Round(best / float64(dur)))
}

// robustMax ignores single-sample spikes (strap dropouts read as 230+):
// the max must be held for at least three samples.
func robustMax(v []int32) int {
	best := 0
	for i := 2; i < len(v); i++ {
		lo := min(v[i], v[i-1], v[i-2])
		if int(lo) > best && lo < 235 {
			best = int(lo)
		}
	}
	return best
}

// ascent sums climbing with 2m hysteresis, which suppresses barometer noise.
func ascent(alt []float32) float64 {
	if len(alt) == 0 {
		return 0
	}
	anchor := float64(alt[0])
	var gain float64
	for _, a := range alt {
		x := float64(a)
		if x > anchor+2 {
			gain += x - anchor
			anchor = x
		} else if x < anchor-2 {
			anchor = x
		}
	}
	return math.Round(gain)
}

// ---- Load ----------------------------------------------------------------------

// PowerTSS is Training Stress Score from normalized power.
func PowerTSS(np, seconds, ftp int) (tss, intensity float64) {
	if np <= 0 || ftp <= 0 || seconds <= 0 {
		return 0, 0
	}
	intensity = float64(np) / float64(ftp)
	tss = float64(seconds) * float64(np) * intensity / (float64(ftp) * 3600) * 100
	return round1(tss), math.Round(intensity*100) / 100
}

// HRTSS approximates TSS from heart rate: each second's intensity is its
// position between resting HR and lactate threshold HR, squared.
func HRTSS(hist []int32, restHR, lthr int) float64 {
	if len(hist) == 0 || lthr <= restHR {
		return 0
	}
	var sum float64
	for bpm, secs := range hist {
		if secs == 0 || bpm <= restHR {
			continue
		}
		f := float64(bpm-restHR) / float64(lthr-restHR)
		if f > 1.25 {
			f = 1.25
		}
		sum += f * f * float64(secs)
	}
	return round1(sum / 3600 * 100)
}

// StrainLoad maps Whoop strain (0-21, logarithmic) onto the TSS scale:
// strain 14 is about a 100 TSS hour at threshold.
func StrainLoad(strain float64) float64 {
	if strain <= 0 {
		return 0
	}
	return round1(100 * math.Pow(strain/14, 2))
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

// PowerZoneSeconds splits time into Coggan's seven zones for ftp.
func PowerZoneSeconds(s *Stream, ftp int) [7]int {
	var z [7]int
	if !s.HasPower() || ftp <= 0 {
		return z
	}
	bounds := []float64{0.55, 0.75, 0.90, 1.05, 1.20, 1.50}
	d := resample(s.T, s.Power)
	for _, seg := range d.segs {
		for _, w := range seg {
			r := w / float64(ftp)
			i := sort.SearchFloat64s(bounds, r)
			if i < len(bounds) && bounds[i] == r {
				i++
			}
			z[i]++
		}
	}
	return z
}

// HRZoneSeconds splits time into five zones by percent of max HR.
func HRZoneSeconds(hist []int32, maxHR int) [6]int {
	var z [6]int
	for bpm, secs := range hist {
		if secs == 0 {
			continue
		}
		pct := float64(bpm) / float64(maxHR)
		i := 0
		for k, lo := range []float64{0.5, 0.6, 0.7, 0.8, 0.9} {
			if pct >= lo {
				i = k + 1
			}
		}
		z[i] += int(secs)
	}
	return z
}
