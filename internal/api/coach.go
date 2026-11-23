package api

import (
	"fmt"
	"math"
	"time"

	"domestique/internal/coach"
	"domestique/internal/store"
)

// coachFacts is what the LLM writes today's call from: the rule-based call
// and the numbers behind it, rounded so the message is rewritten only when
// something worth saying has changed.
func (a *API) coachFacts(t *todayResp, day time.Time) coach.Facts {
	f := coach.Facts{Date: t.Date, Level: t.Readiness.Level, Title: t.Readiness.Title, Detail: t.Readiness.Detail}
	ip := func(v float64) *int { n := int(math.Round(v)); return &n }
	if rec := t.Recovery; rec != nil {
		f.Recovery = &rec.Score
		if rec.HRVms > 0 {
			f.HRVms = ip(rec.HRVms)
		}
		if rec.RestingHR > 0 {
			f.RestingHR = &rec.RestingHR
		}
	}
	if sl := t.Sleep; sl != nil && sl.AsleepS > 0 {
		h := math.Round(float64(sl.AsleepS)/360) / 10
		f.SleepH = &h
		f.SleepPerf = sl.Performance
	}
	// Like the rules, only speak of load once there is enough history for it.
	if d := t.Load; d != nil && d.CTL > 5 {
		f.Fitness, f.Fatigue, f.Form = ip(d.CTL), ip(d.ATL), ip(d.TSB)
		f.Ramp = ip(t.RampCTL)
	}
	if p := t.Plan; p != nil {
		f.Plan = p.Name
		for _, e := range p.Exercises {
			f.PlanSets += len(e.Sets)
		}
	}
	f.Felt = t.felt
	changes(&f, t.day)
	if v := t.Ride; v != nil {
		f.Ride = rideFact(v.RidePlan)
		f.RideDone = v.Done != nil
	}
	today := day.Format("2006-01-02")
	for _, x := range t.Latest {
		at := time.UnixMilli(x.StartAt).In(a.loc())
		ago := int(math.Round(day.Sub(time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, a.loc())).Hours() / 24))
		var when string
		switch {
		case at.Format("2006-01-02") == today:
			when = "today"
		case ago == 1:
			when = "yesterday"
		case ago == 2 || ago == 3:
			when = fmt.Sprintf("%d days ago", ago)
		default:
			continue // older than that says nothing about today
		}
		what := x.Name
		if what == "" {
			what = x.Sport
		}
		secs := x.MovingS
		if secs <= 0 {
			secs = x.ElapsedS
		}
		ev := coach.Event{When: when, What: what, Min: int(math.Round(float64(secs) / 60))}
		if x.TSS > 0 {
			ev.TSS = int(math.Round(x.TSS))
		}
		f.Recent = append(f.Recent, ev)
	}
	return f
}

// changes fills in the norm, the earlier mornings, and what sets this
// morning apart from them. Small moves are left out: Whoop's HRV swings 10%
// from night to night, and the score by about as many points.
func changes(f *coach.Facts, d dayState) {
	m := d.Morning
	if b := m.Base; b != nil {
		f.HRVNorm, f.RHRNorm = ip(b.HRVms), ip(b.RestingHR)
	}
	prev := d.Mornings
	if len(prev) > 3 {
		prev = prev[len(prev)-3:]
	}
	for _, x := range prev {
		f.Previous = append(f.Previous, x.Score)
	}
	rec := d.Rec
	if rec == nil {
		return
	}
	var out []string
	var yday *store.Recovery
	if n := len(d.Mornings); n > 0 {
		// Only the morning right before counts as yesterday.
		y := d.Mornings[n-1]
		if t, err := time.Parse("2006-01-02", rec.Date); err == nil && t.AddDate(0, 0, -1).Format("2006-01-02") == y.Date {
			yday = &y
		}
	}
	steady := true
	if yday != nil {
		switch diff := rec.Score - yday.Score; {
		case diff >= 15:
			out = append(out, fmt.Sprintf("recovery %d%%, up from %d%% yesterday", rec.Score, yday.Score))
			steady = false
		case diff <= -15:
			out = append(out, fmt.Sprintf("recovery %d%%, down from %d%% yesterday", rec.Score, yday.Score))
			steady = false
		case diff > 10 || diff < -10:
			steady = false
		}
	}
	if len(f.Previous) == 3 && yday != nil {
		p := f.Previous
		switch {
		case p[0] < p[1] && p[1] < p[2] && p[2] < rec.Score && rec.Score-p[0] >= 15:
			out = append(out, fmt.Sprintf("recovery has risen four mornings running, from %d%%", p[0]))
			steady = false
		case p[0] > p[1] && p[1] > p[2] && p[2] > rec.Score && p[0]-rec.Score >= 15:
			out = append(out, fmt.Sprintf("recovery has fallen four mornings running, from %d%%", p[0]))
			steady = false
		}
	}
	if pct, ok := m.HRVPct(); ok {
		switch n := int(math.Round(pct)); {
		case n >= 10:
			out = append(out, fmt.Sprintf("HRV %.0f ms, %d%% above your norm of %.0f", rec.HRVms, n, m.Base.HRVms))
			steady = false
		case n <= -10:
			out = append(out, fmt.Sprintf("HRV %.0f ms, %d%% under your norm of %.0f", rec.HRVms, -n, m.Base.HRVms))
			steady = false
		}
	}
	if dr, ok := m.RHRDelta(); ok {
		switch n := int(math.Round(dr)); {
		case n >= 3:
			out = append(out, fmt.Sprintf("resting heart rate %d, %d above your norm of %.0f", rec.RestingHR, n, m.Base.RestingHR))
			steady = false
		case n <= -3:
			out = append(out, fmt.Sprintf("resting heart rate %d, %d under your norm of %.0f", rec.RestingHR, -n, m.Base.RestingHR))
			steady = false
		}
	}
	if m.Short() {
		line := fmt.Sprintf("a short night: %.1f h asleep", m.SleepH)
		if b := m.Base; b != nil && b.SleepH > 0 {
			line += fmt.Sprintf(", against your usual %.1f", b.SleepH)
		}
		out = append(out, line)
		steady = false
	}
	if steady && yday != nil {
		out = append(out, "a morning much like the last few")
	}
	// The three that matter most are plenty for one or two sentences.
	if len(out) > 3 {
		out = out[:3]
	}
	f.Changed = out
}
