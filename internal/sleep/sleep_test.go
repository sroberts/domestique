package sleep

import (
	"fmt"
	"testing"
	"time"

	"domestique/internal/load"
	"domestique/internal/store"
)

var utc = time.UTC

// night builds a night ending on date, in bed from bed ("23:30", the day
// before if after noon) for asleep hours, with a 7.5 h baseline need.
func night(date, bed string, asleepH float64) store.Sleep {
	d, _ := time.ParseInLocation("2006-01-02", date, utc)
	b, _ := time.Parse("15:04", bed)
	start := time.Date(d.Year(), d.Month(), d.Day(), b.Hour(), b.Minute(), 0, 0, utc)
	if b.Hour() >= 12 {
		start = start.AddDate(0, 0, -1)
	}
	asleep := int(asleepH * 3600)
	return store.Sleep{ID: "whoop:" + date, Date: date, StartAt: start.UnixMilli(),
		EndAt: start.Add(time.Duration(asleep+1800) * time.Second).UnixMilli(), AsleepS: asleep, Source: "whoop",
		Need: &store.SleepNeed{BaselineS: 27000}}
}

func day(i int) string {
	return time.Date(2026, 11, 1, 0, 0, 0, 0, utc).AddDate(0, 0, i).Format("2006-01-02")
}

// Bedtimes either side of midnight are close, not 23 hours apart.
func TestBedtimeAcrossMidnight(t *testing.T) {
	ns := []store.Sleep{night(day(1), "23:30", 7), night(day(2), "00:30", 7), night(day(3), "23:50", 7)}
	tr := Build(Input{Today: day(3), Weeks: 2, Nights: ns, Loc: utc})
	if tr.Bed.Median != "23:50" || tr.Bed.SpreadMin != 20 || tr.Bed.Nights != 3 {
		t.Fatalf("bed %+v, want 23:50 ±20", tr.Bed)
	}
	if len(tr.Nights) != 3 || tr.Nights[1].Bed != "00:30" {
		t.Fatalf("nights %+v", tr.Nights)
	}
}

// Debt is the last seven nights against baseline: long nights pay some
// back, and it never goes negative.
func TestDebt(t *testing.T) {
	var ns []store.Sleep
	for i, h := range []float64{9, 9, 6.5, 6.5, 7.5, 6, 7.5, 7} { // the first falls outside the seven
		ns = append(ns, night(day(i+1), "23:00", h))
	}
	tr := Build(Input{Today: day(8), Weeks: 2, Nights: ns, Loc: utc})
	// 7.5 h baseline: -1.5, +1, +1, 0, +1.5, 0, +0.5 = 2.5 h
	if tr.Debt.S != 9000 || tr.Debt.Nights != 7 {
		t.Fatalf("debt %+v, want 2.5 h over 7 nights", tr.Debt)
	}
	long := Build(Input{Today: day(3), Weeks: 2, Nights: ns[:2], Loc: utc})
	if long.Debt.S != 0 {
		t.Fatalf("long nights left debt %d, want 0", long.Debt.S)
	}
	// Ten days without the strap: nights from before the gap don't count.
	gap := Build(Input{Today: day(18), Weeks: 4, Nights: ns, Loc: utc})
	if gap.Debt.S != 0 || gap.Debt.Nights != 0 {
		t.Fatalf("debt after a gap %+v, want none", gap.Debt)
	}
}

// Comparisons wait for five nights a side, and pair a night with the
// recovery filed under the same date.
func TestShortNightsCompare(t *testing.T) {
	var ns []store.Sleep
	var rec []store.Recovery
	for i := range 10 {
		h, score := 7.5, 70
		if i%2 == 0 {
			h, score = 6, 40 // 1.5 h short
		}
		ns = append(ns, night(day(i+1), "23:00", h))
		rec = append(rec, store.Recovery{Date: day(i + 1), Score: score})
	}
	c := Build(Input{Today: day(10), Weeks: 2, Nights: ns, Recovery: rec, Loc: utc}).ShortNights
	if !c.Ready || c.With.Nights != 5 || c.With.Median != 40 || c.Without.Median != 70 {
		t.Fatalf("compare %+v", c)
	}
	c = Build(Input{Today: day(10), Weeks: 2, Nights: ns[:9], Recovery: rec, Loc: utc}).ShortNights
	if c.Ready {
		t.Fatalf("ready with %d and %d nights", c.With.Nights, c.Without.Nights)
	}
}

// A heavy day is judged against that day's fitness, and its night is the
// one that ends the next morning.
func TestHeavyDaysCompare(t *testing.T) {
	var ns []store.Sleep
	var days []load.Day
	for i := range 12 {
		tss, h := 40.0, 7.0
		if i%2 == 0 {
			tss, h = 120, 6.2
		}
		days = append(days, load.Day{Date: day(i), TSS: tss, CTL: 60})
		ns = append(ns, night(day(i+1), "23:00", h))
	}
	days = append(days, load.Day{Date: day(12), TSS: 500, CTL: 3}) // no fitness yet: not counted
	ns = append(ns, night(day(13), "23:00", 4))
	c := Build(Input{Today: day(13), Weeks: 2, Nights: ns, Load: days, Loc: utc}).HeavyDays
	if !c.Ready || c.With.Nights != 6 || c.With.Median != 6.2 || c.Without.Median != 7 || c.Without.Nights != 6 {
		t.Fatalf("compare %+v", c)
	}
}

// The last night comes with its naps and the norms from the nights before it.
func TestLastAndNorms(t *testing.T) {
	var ns []store.Sleep
	for i := range 5 {
		x := night(day(i+1), "23:00", 7)
		rr, eff := 15.0+float64(i)*0.1, 90+i
		x.RespiratoryRate, x.Efficiency, x.DeepS, x.REMS = &rr, &eff, 5000+i*100, 6000
		ns = append(ns, x)
	}
	nap := store.Sleep{ID: "whoop:nap", Date: day(5), Nap: true, AsleepS: 1200}
	tr := Build(Input{Today: day(5), Weeks: 2, Nights: ns, Naps: []store.Sleep{nap}, Loc: utc})
	if tr.Last == nil || tr.Last.Date != day(5) || len(tr.LastNaps) != 1 {
		t.Fatalf("last %+v naps %d", tr.Last, len(tr.LastNaps))
	}
	n := tr.Norms // from the four nights before
	if n.Nights != 4 || *n.RespiratoryRate != 15.2 || *n.Efficiency != 92 || n.DeepS != 5150 {
		t.Fatalf("norms %+v rr %v eff %v", n, *n.RespiratoryRate, *n.Efficiency)
	}
	if got := fmt.Sprint(tr.AvgAsleepS); got != "25200" {
		t.Fatalf("average %s", got)
	}
}

// Only logged evenings count: an evening with no log is unknown, not a
// "without". Items need eight logged evenings a side.
func TestEveningsOnlyLogged(t *testing.T) {
	var ns []store.Sleep
	var evs []store.Evening
	var rec []store.Recovery
	for i := range 20 {
		h, score := 7.5, 70
		items := []string{}
		if i%2 == 0 {
			items, h, score = []string{"alcohol"}, 6, 45
		}
		ns = append(ns, night(day(i+1), "23:00", h))
		rec = append(rec, store.Recovery{Date: day(i + 1), Score: score})
		if i < 16 { // the last four evenings weren't logged
			evs = append(evs, store.Evening{Date: day(i), Items: items})
		}
	}
	tr := Build(Input{Today: day(20), Weeks: 2, Nights: ns, Recovery: rec, Evenings: evs, Loc: utc})
	var alc EveningCompare
	for _, c := range tr.Evenings {
		if c.Key == "alcohol" {
			alc = c
		}
	}
	if !alc.Ready || alc.With.Nights != 8 || alc.Without.Nights != 8 || alc.With.AsleepH != 6 || alc.Without.AsleepH != 7.5 {
		t.Fatalf("alcohol %+v", alc)
	}
	if *alc.With.Recovery != 45 || *alc.Without.Recovery != 70 || tr.EveningsLogged != 16 {
		t.Fatalf("recovery %v/%v logged %d", *alc.With.Recovery, *alc.Without.Recovery, tr.EveningsLogged)
	}
	short := Build(Input{Today: day(20), Weeks: 2, Nights: ns, Recovery: rec, Evenings: evs[:15], Loc: utc})
	for _, c := range short.Evenings {
		if c.Key == "alcohol" && c.Ready {
			t.Fatalf("ready with %d and %d", c.With.Nights, c.Without.Nights)
		}
	}
}

// Late training is filled in: a workout ending within three hours of that
// night's bedtime, not one earlier in the day.
func TestLateTraining(t *testing.T) {
	n := night(day(2), "23:00", 7) // in bed 23:00 on day(1)
	bed := time.UnixMilli(n.StartAt)
	evs := []store.Evening{{Date: day(1), Items: []string{}}}
	cases := []struct {
		end  time.Time
		late bool
	}{
		{bed.Add(-90 * time.Minute), true},
		{bed.Add(-4 * time.Hour), false},
		{bed.Add(30 * time.Minute), false}, // after bed: not before this night
	}
	for _, c := range cases {
		tr := Build(Input{Today: day(2), Weeks: 2, Nights: []store.Sleep{n}, Evenings: evs, Workouts: []Workout{{EndAt: c.end.UnixMilli()}}, Loc: utc})
		got := tr.Evenings[len(tr.Evenings)-1]
		if got.Key != "late_training" || (got.With.Nights == 1) != c.late {
			t.Errorf("ended %s before bed: %+v, want late=%v", bed.Sub(c.end), got, c.late)
		}
	}
}

// After midnight, before tonight is scored, the screen's debt is the one
// tonight's bedtime counts: the seven nights up to last night.
func TestDebtAgreesWithBedtime(t *testing.T) {
	ns := nightsWaking(14, "06:30", 6.5) // 1 h short a night
	screen := Build(Input{Today: day(15), Weeks: 2, Nights: ns, Loc: utc}).Debt
	b := PlanBedtime(BedtimeInput{Date: day(14), Nights: ns, Loc: utc})
	if screen.S != 7*3600 || screen.Nights != 7 || b.Why != "7h 00 short this week." {
		t.Fatalf("screen %+v, bedtime %q", screen, b.Why)
	}
}
