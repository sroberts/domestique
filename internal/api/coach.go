package api

import (
	"fmt"
	"math"
	"time"

	"domestique/internal/coach"
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
