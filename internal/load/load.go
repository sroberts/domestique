// Package load models training load across every sport: TSS per activity,
// and fitness (CTL), fatigue (ATL) and form (TSB) per day.
package load

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strconv"
	"time"

	"domestique/internal/activity"
	"domestique/internal/store"
)

// Athlete holds the thresholds load is computed against.
type Athlete struct {
	FTP         int    `json:"ftp"`
	FTPSource   string `json:"ftp_source"` // override | detected | none
	MaxHR       int    `json:"max_hr"`
	MaxHRSource string `json:"max_hr_source"`
	RestHR      int    `json:"rest_hr"`
	LTHR        int    `json:"lthr"`
}

const (
	ftpWindow   = 120 * 24 * time.Hour
	idx20min    = 11 // activity.CurveDurations[11] == 1200
	defaultMax  = 190
	defaultRest = 55
)

func intKV(ctx context.Context, st *store.Store, key string) int {
	v, err := st.Athlete(ctx, key)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(v)
	return n
}

// Current returns today's thresholds without recomputing anything.
func Current(ctx context.Context, st *store.Store, fallbackMaxHR int) Athlete {
	a := Athlete{FTPSource: "none", MaxHRSource: "default"}
	if v := intKV(ctx, st, "ftp_override"); v > 0 {
		a.FTP, a.FTPSource = v, "override"
	} else if v := intKV(ctx, st, "ftp_detected"); v > 0 {
		a.FTP, a.FTPSource = v, "detected"
	}
	a.MaxHR = fallbackMaxHR
	if a.MaxHR <= 0 {
		a.MaxHR = defaultMax
	}
	if v := intKV(ctx, st, "max_hr_override"); v > 0 {
		a.MaxHR, a.MaxHRSource = v, "override"
	} else if v := intKV(ctx, st, "max_hr_whoop"); v > 0 {
		a.MaxHR, a.MaxHRSource = v, "whoop"
	} else if v := intKV(ctx, st, "max_hr_detected"); v > 0 {
		a.MaxHR, a.MaxHRSource = v, "detected"
	}
	a.RestHR = intKV(ctx, st, "rest_hr_detected")
	if a.RestHR <= 0 {
		a.RestHR = defaultRest
	}
	a.LTHR = int(math.Round(float64(a.MaxHR) * 0.9))
	return a
}

type metricsLite struct {
	NP           int     `json:"np"`
	MaxHR        int     `json:"max_hr"`
	PowerCurve   []int   `json:"power_curve"`
	HRHist       []int32 `json:"hr_hist"`
	PowerSeconds int     `json:"power_seconds"`
}

// Recalc re-derives thresholds from all data and rescores every activity.
func Recalc(ctx context.Context, st *store.Store, fallbackMaxHR int) (Athlete, error) {
	acts, err := st.ListActivities(ctx, store.ActivityQuery{})
	if err != nil {
		return Athlete{}, err
	}
	sort.Slice(acts, func(i, j int) bool { return acts[i].StartAt < acts[j].StartAt })
	ms := make([]metricsLite, len(acts))
	for i, a := range acts {
		_ = json.Unmarshal(a.Metrics, &ms[i])
	}

	// Max HR: the second-highest activity max in the last three years drops a
	// single strap glitch.
	var maxes []int
	cutoff := time.Now().AddDate(-3, 0, 0).UnixMilli()
	for i, a := range acts {
		if a.StartAt >= cutoff && ms[i].MaxHR >= 120 {
			maxes = append(maxes, ms[i].MaxHR)
		}
	}
	if len(maxes) < 2 {
		// An older history: use all of it rather than detect nothing.
		maxes = maxes[:0]
		for i := range acts {
			if ms[i].MaxHR >= 120 {
				maxes = append(maxes, ms[i].MaxHR)
			}
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(maxes)))
	if len(maxes) >= 2 {
		if err := st.SetAthlete(ctx, "max_hr_detected", strconv.Itoa(maxes[1])); err != nil {
			return Athlete{}, err
		}
	} else if len(maxes) == 1 {
		_ = st.SetAthlete(ctx, "max_hr_detected", strconv.Itoa(maxes[0]))
	}

	// Resting HR: median of the last 30 morning readings.
	recs, err := st.RecoveryRange(ctx, time.Now().AddDate(0, 0, -30).Format("2006-01-02"), "9999-12-31")
	if err != nil {
		return Athlete{}, err
	}
	if len(recs) > 0 {
		rhr := make([]int, 0, len(recs))
		for _, r := range recs {
			if r.RestingHR > 0 {
				rhr = append(rhr, r.RestingHR)
			}
		}
		if len(rhr) > 0 {
			sort.Ints(rhr)
			_ = st.SetAthlete(ctx, "rest_hr_detected", strconv.Itoa(rhr[len(rhr)/2]))
		}
	}

	ath := Current(ctx, st, fallbackMaxHR)
	override := ath.FTPSource == "override"

	// Rolling FTP: 95% of the best 20-minute power in the trailing window, so
	// each ride is scored against the fitness of its own day.
	type effort struct {
		at   int64
		best int
	}
	var window []effort
	lastFTP := 0
	// Workouts with no heart rate stream: Whoop's time in zones stands in.
	var noHR []string
	for i, a := range acts {
		if len(ms[i].HRHist) == 0 {
			noHR = append(noHR, a.ID)
		}
	}
	srcs, err := st.SourcesForMany(ctx, noHR)
	if err != nil {
		return Athlete{}, err
	}
	for i, a := range acts {
		if z, ok := store.WhoopZones(srcs[a.ID]); ok && len(ms[i].HRHist) == 0 {
			ms[i].HRHist = activity.ZoneHRHist(z, ath.MaxHR)
		}
	}

	for i, a := range acts {
		m := ms[i]
		if activity.IsRide(a.Sport) && len(m.PowerCurve) > idx20min && m.PowerCurve[idx20min] > 0 {
			window = append(window, effort{a.StartAt, m.PowerCurve[idx20min]})
		}
		for len(window) > 0 && a.StartAt-window[0].at > ftpWindow.Milliseconds() {
			window = window[1:]
		}
		ftp := lastFTP
		if len(window) > 0 {
			best := 0
			for _, e := range window {
				best = max(best, e.best)
			}
			ftp = int(math.Round(float64(best) * 0.95))
		}
		lastFTP = ftp
		if override {
			ftp = ath.FTP
		}

		tss, intensity, method := score(a, m, ftp, ath)
		if a.FTP != ftp || a.TSS != tss || a.Intensity != intensity || a.LoadMethod != method {
			if err := st.SetLoad(ctx, a.ID, ftp, tss, intensity, method); err != nil {
				return Athlete{}, err
			}
		}
	}
	if lastFTP > 0 {
		if err := st.SetAthlete(ctx, "ftp_detected", strconv.Itoa(lastFTP)); err != nil {
			return Athlete{}, err
		}
	}
	return Current(ctx, st, fallbackMaxHR), nil
}

func score(a store.Activity, m metricsLite, ftp int, ath Athlete) (float64, float64, string) {
	moving := a.MovingS
	if moving <= 0 {
		moving = a.ElapsedS
	}
	switch {
	case m.NP > 0 && ftp > 0 && m.PowerSeconds*2 >= moving:
		tss, ifv := activity.PowerTSS(m.NP, moving, ftp)
		return tss, ifv, "power"
	case a.Sport == activity.SportStrength && a.Strain != nil:
		return activity.StrainLoad(*a.Strain, moving), 0, "strain"
	case len(m.HRHist) > 0:
		return activity.HRTSS(m.HRHist, ath.RestHR, ath.LTHR), 0, "hr"
	case a.Strain != nil:
		return activity.StrainLoad(*a.Strain, moving), 0, "strain"
	}
	return 0, 0, "none"
}

// Day is one day of the fitness model.
type Day struct {
	Date        string  `json:"date"`
	TSS         float64 `json:"tss"`
	RideTSS     float64 `json:"ride_tss"`
	StrengthTSS float64 `json:"strength_tss"`
	OtherTSS    float64 `json:"other_tss"`
	CTL         float64 `json:"ctl"` // fitness, 42-day
	ATL         float64 `json:"atl"` // fatigue, 7-day
	TSB         float64 `json:"tsb"` // form: yesterday's fitness minus fatigue
	Hours       float64 `json:"hours"`
}

// Daily runs the fitness model from the first activity through `to`, and
// returns the days in [from, to]. Dates are in loc.
func Daily(ctx context.Context, st *store.Store, from, to time.Time, loc *time.Location) ([]Day, error) {
	acts, err := st.ListActivities(ctx, store.ActivityQuery{To: to.Add(24 * time.Hour).UnixMilli()})
	if err != nil {
		return nil, err
	}
	type agg struct{ tss, ride, strength, other, hours float64 }
	byDay := map[string]*agg{}
	first := from
	for _, a := range acts {
		t := time.UnixMilli(a.StartAt).In(loc)
		d := t.Format("2006-01-02")
		g := byDay[d]
		if g == nil {
			g = &agg{}
			byDay[d] = g
		}
		g.tss += a.TSS
		g.hours += float64(a.MovingS) / 3600
		switch {
		case activity.IsRide(a.Sport):
			g.ride += a.TSS
		case a.Sport == activity.SportStrength:
			g.strength += a.TSS
		default:
			g.other += a.TSS
		}
		if t.Before(first) {
			first = t
		}
	}
	var out []Day
	var ctl, atl float64
	day := time.Date(first.Year(), first.Month(), first.Day(), 0, 0, 0, 0, loc)
	end := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, loc)
	fromDay := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, loc)
	for !day.After(end) {
		d := day.Format("2006-01-02")
		var g agg
		if p := byDay[d]; p != nil {
			g = *p
		}
		tsb := ctl - atl
		ctl += (g.tss - ctl) / 42
		atl += (g.tss - atl) / 7
		if !day.Before(fromDay) {
			out = append(out, Day{
				Date: d, TSS: r1(g.tss), RideTSS: r1(g.ride), StrengthTSS: r1(g.strength), OtherTSS: r1(g.other),
				CTL: r1(ctl), ATL: r1(atl), TSB: r1(tsb), Hours: math.Round(g.hours*100) / 100,
			})
		}
		day = day.AddDate(0, 0, 1)
	}
	return out, nil
}

func r1(v float64) float64 { return math.Round(v*10) / 10 }

// ErrNoData is returned when there is nothing to model yet.
var ErrNoData = errors.New("no activities")
