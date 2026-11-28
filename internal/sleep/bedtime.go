package sleep

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"domestique/internal/load"
	"domestique/internal/store"
)

// Bedtime is tonight's advice: when to be in bed to wake at the usual time
// having slept what tonight is likely to need. It's advice only; readiness
// never reads it.
type Bedtime struct {
	Date     string `json:"date"`      // the evening
	BedBy    string `json:"bed_by"`    // "22:40", to the nearest five minutes
	Wake     string `json:"wake"`      // "06:45"
	WakeFrom string `json:"wake_from"` // "set" (Settings) or "usual" (the last 14 nights)
	NeedS    int    `json:"need_s"`    // tonight's estimated need
	InBedS   int    `json:"in_bed_s"`  // need over the usual efficiency
	// The parts of the estimate.
	BaselineS int    `json:"baseline_s"`
	DebtS     int    `json:"debt_s"`  // a third of the week's debt, at most an hour
	HeavyS    int    `json:"heavy_s"` // after a heavy training day
	Why       string `json:"why"`     // "Hard training day, and 1:12 short this week."
}

// Starting values for tonight's need, to tune against the need Whoop scores
// each morning (see Estimates).
const (
	DebtShare = 3    // a third of the week's debt
	DebtCapS  = 3600 // at most an hour of it
	HeavyS    = 1800 // half an hour more after a heavy day
)

// Weekdays are the keys for wake times set in Settings.
var Weekdays = []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}

// BedtimeInput is what PlanBedtime reads.
type BedtimeInput struct {
	Date   string            // the evening, YYYY-MM-DD
	Nights []store.Sleep     // recent nights, at least the last two weeks
	Today  *load.Day         // the evening's day of training, if any
	Wake   map[string]string // wake times set in Settings, by weekday ("mon": "06:30")
	Loc    *time.Location
}

// PlanBedtime works out tonight's bedtime, or nil when there's nothing to
// go on yet (no night with a sleep need scored).
func PlanBedtime(in BedtimeInput) *Bedtime {
	loc := in.Loc
	if loc == nil {
		loc = time.Local
	}
	eve, err := time.ParseInLocation("2006-01-02", in.Date, loc)
	if err != nil {
		return nil
	}
	nights := append([]store.Sleep(nil), in.Nights...)
	sort.Slice(nights, func(i, j int) bool { return nights[i].StartAt < nights[j].StartAt })
	var before []store.Sleep // nights that ended by this evening
	for _, n := range nights {
		if n.Date <= in.Date {
			before = append(before, n)
		}
	}
	b := &Bedtime{Date: in.Date}
	for i := len(before) - 1; i >= 0; i-- {
		if n := before[i].Need; n != nil && n.BaselineS > 0 {
			b.BaselineS = n.BaselineS
			break
		}
	}
	if b.BaselineS == 0 {
		return nil
	}
	recent := before[max(0, len(before)-14):]

	// Wake: set in Settings for tomorrow's weekday, or the usual wake on
	// that kind of day (weekday or weekend) over the last 14 nights.
	morning := eve.AddDate(0, 0, 1)
	key := Weekdays[morning.Weekday()]
	if t, ok := in.Wake[key]; ok && validClock(t) {
		b.Wake, b.WakeFrom = t, "set"
	} else {
		weekend := func(d time.Weekday) bool { return d == time.Saturday || d == time.Sunday }
		var same, all []float64
		for _, n := range recent {
			end := time.UnixMilli(n.EndAt).In(loc)
			m := float64(fromMidnight(end))
			all = append(all, m)
			if weekend(end.Weekday()) == weekend(morning.Weekday()) {
				same = append(same, m)
			}
		}
		if len(same) < 3 {
			same = all
		}
		if len(same) == 0 {
			return nil
		}
		b.Wake, b.WakeFrom = clockOf(int(math.Round(median(same)))), "usual"
	}

	// Tonight's need: the usual, some of the week's debt, more after a hard day.
	d := debt(before)
	b.DebtS = min(d.S/DebtShare, DebtCapS)
	heavy := in.Today != nil && in.Today.CTL > 5 && in.Today.TSS >= HeavyDayLoad*in.Today.CTL
	if heavy {
		b.HeavyS = HeavyS
	}
	b.NeedS = b.BaselineS + b.DebtS + b.HeavyS

	// In bed long enough to sleep that, at the usual efficiency.
	var eff []float64
	for _, n := range recent {
		if n.Efficiency != nil && *n.Efficiency > 50 {
			eff = append(eff, float64(*n.Efficiency)/100)
		}
	}
	e := 0.9
	if len(eff) > 0 {
		e = median(eff)
	}
	b.InBedS = int(math.Round(float64(b.NeedS) / e))
	wake := parseClock(b.Wake)
	bed := ((wake-int(math.Round(float64(b.InBedS)/60)))%(24*60) + 24*60) % (24 * 60)
	bed = (bed + 2) / 5 * 5 % (24 * 60)
	b.BedBy = clockOf(bed)

	var why []string
	if heavy {
		why = append(why, "Hard training day")
	}
	if d.S >= 1800 {
		why = append(why, fmt.Sprintf("%s short this week", hmText(d.S)))
	}
	if len(why) == 0 {
		b.Why = "Your usual need, nothing owed this week."
	} else {
		w := strings.Join(why, ", and ")
		b.Why = strings.ToUpper(w[:1]) + w[1:] + "."
	}
	return b
}

func validClock(t string) bool {
	_, err := time.Parse("15:04", t)
	return err == nil && len(t) == 5
}

func parseClock(t string) int {
	c, _ := time.Parse("15:04", t)
	return c.Hour()*60 + c.Minute()
}

func clockOf(m int) string { return fmt.Sprintf("%02d:%02d", m/60%24, m%60) }

// hmText is "1:12" for a stretch of time.
func hmText(s int) string {
	m := (s + 30) / 60
	return fmt.Sprintf("%d:%02d", m/60, m%60)
}

// Estimate is one night's estimated need, kept to check against what Whoop
// scored the next morning.
type Estimate struct {
	Date  string `json:"date"` // the evening
	NeedS int    `json:"need_s"`
}

// EstimateCheck is how the estimates did: the median of (scored − estimated).
type EstimateCheck struct {
	Nights int `json:"nights"`
	OffS   int `json:"off_s"` // positive: Whoop scored more than estimated
}

// CheckEstimates compares kept estimates with the need Whoop scored for the
// night after each evening.
func CheckEstimates(est []Estimate, nights []store.Sleep, loc *time.Location) EstimateCheck {
	need := map[string]int{}
	for _, n := range nights {
		if n.Need != nil {
			need[n.Date] = n.Need.TotalS()
		}
	}
	var off []float64
	for _, e := range est {
		d, err := time.ParseInLocation("2006-01-02", e.Date, loc)
		if err != nil {
			continue
		}
		if s, ok := need[d.AddDate(0, 0, 1).Format("2006-01-02")]; ok {
			off = append(off, float64(s-e.NeedS))
		}
	}
	return EstimateCheck{Nights: len(off), OffS: int(math.Round(median(off)))}
}
