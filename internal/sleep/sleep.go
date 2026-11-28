// Package sleep turns stored nights into what the Sleep screen and the MCP
// tools show: how each night went against need, how regular bed and wake
// times are, the week's sleep debt, and plain comparisons with recovery and
// training. It only reads what it's given; nothing here touches the store.
package sleep

import (
	"math"
	"sort"
	"time"

	"domestique/internal/load"
	"domestique/internal/store"
)

// Night is one night as the screen lists it.
type Night struct {
	Date       string `json:"date"` // wake-up date
	AsleepS    int    `json:"asleep_s"`
	NeedS      int    `json:"need_s,omitempty"`     // Whoop's whole need; 0 when not scored
	BaselineS  int    `json:"baseline_s,omitempty"` // the part of need that is the athlete's norm
	Bed        string `json:"bed"`                  // "23:12", local
	Wake       string `json:"wake"`                 // "06:58", local
	Efficiency *int   `json:"efficiency,omitempty"`
}

// Clock is a typical time and how far nights stray from it.
type Clock struct {
	Median    string `json:"median"`     // "23:10"
	SpreadMin int    `json:"spread_min"` // median minutes from it
	Nights    int    `json:"nights"`
}

// Debt is the last seven nights' shortfall against baseline need. Baseline,
// not total need: total need already includes Whoop's own debt.
type Debt struct {
	S      int `json:"s"`      // never below zero
	Nights int `json:"nights"` // nights with a baseline among the seven
}

// Norms are the athlete's usual numbers, for "against your norm".
type Norms struct {
	Nights          int      `json:"nights"`
	RespiratoryRate *float64 `json:"respiratory_rate,omitempty"`
	DeepS           int      `json:"deep_s"`
	REMS            int      `json:"rem_s"`
	Efficiency      *int     `json:"efficiency,omitempty"`
}

// Side is one half of a comparison.
type Side struct {
	Nights int     `json:"nights"`
	Median float64 `json:"median"`
}

// Compare is a plain comparison: medians and counts, never a cause.
type Compare struct {
	With    Side `json:"with"`
	Without Side `json:"without"`
	// Ready once both sides have MinCompare nights: fewer say nothing.
	Ready bool `json:"ready"`
}

// MinCompare is the fewest nights on each side before a comparison shows.
const MinCompare = 5

// Trends is the Sleep screen's whole picture.
type Trends struct {
	Weeks        int           `json:"weeks"`
	Last         *store.Sleep  `json:"last"`
	LastNaps     []store.Sleep `json:"last_naps"`
	Norms        Norms         `json:"norms"`
	Nights       []Night       `json:"nights"`
	AvgAsleepS   int           `json:"avg_asleep_s"`
	AvgNeedS     int           `json:"avg_need_s,omitempty"`
	Bed          Clock         `json:"bed"`
	Wake         Clock         `json:"wake"`
	Debt         Debt          `json:"debt"`
	ShortNights  Compare       `json:"short_nights"` // next-morning recovery %, after a short night or not
	HeavyDays    Compare       `json:"heavy_days"`   // hours asleep, after a heavy day or not
	ShortNightS  int           `json:"short_night_s"`
	HeavyDayLoad float64       `json:"heavy_day_load"`
	// What goes with the nights after logged evenings, item by item.
	Evenings       []EveningCompare `json:"evenings"`
	EveningsLogged int              `json:"evenings_logged"`
	MinEvening     int              `json:"min_evening"`
	// How the bedtime's estimated need compared with what Whoop scored.
	Estimates EstimateCheck `json:"estimates"`
}

// ShortNightS is how far under baseline need a night must be to count as
// short in the comparison: an hour.
const ShortNightS = 3600

// HeavyDayLoad is how much a day's load must be, as a multiple of fitness,
// to count as heavy.
const HeavyDayLoad = 1.5

// Input is what Build reads. Nights and Recovery span the comparison window
// (a year is plenty); Load covers the same days.
type Input struct {
	Today    string // the athlete's date
	Weeks    int    // 2, 4 or 12
	Nights   []store.Sleep
	Naps     []store.Sleep
	Recovery []store.Recovery
	Load     []load.Day
	Evenings []store.Evening // logged evenings over the comparison window
	Workouts []Workout       // when workouts ended, for late training
	Loc      *time.Location
}

// Build works out the Sleep screen's picture.
func Build(in Input) Trends {
	loc := in.Loc
	if loc == nil {
		loc = time.Local
	}
	nights := append([]store.Sleep(nil), in.Nights...)
	sort.Slice(nights, func(i, j int) bool { return nights[i].StartAt < nights[j].StartAt })
	t := Trends{Weeks: in.Weeks, Nights: []Night{}, LastNaps: []store.Sleep{}, ShortNightS: ShortNightS, HeavyDayLoad: HeavyDayLoad}

	if n := len(nights); n > 0 {
		last := nights[n-1]
		t.Last = &last
		for _, x := range in.Naps {
			if x.Date == last.Date {
				t.LastNaps = append(t.LastNaps, x)
			}
		}
		t.Norms = norms(nights[max(0, n-1-28) : n-1])
	}

	today, err := time.ParseInLocation("2006-01-02", in.Today, loc)
	if err != nil {
		today = time.Now().In(loc)
	}
	from := today.AddDate(0, 0, -7*in.Weeks+1).Format("2006-01-02")
	var beds, wakes []int
	var asleep, need, withNeed int
	for _, x := range nights {
		if x.Date < from || x.Date > in.Today {
			continue
		}
		start, end := time.UnixMilli(x.StartAt).In(loc), time.UnixMilli(x.EndAt).In(loc)
		row := Night{Date: x.Date, AsleepS: x.AsleepS, Bed: start.Format("15:04"), Wake: end.Format("15:04"), Efficiency: x.Efficiency}
		if x.Need != nil {
			row.NeedS, row.BaselineS = x.Need.TotalS(), x.Need.BaselineS
			need += row.NeedS
			withNeed++
		}
		t.Nights = append(t.Nights, row)
		asleep += x.AsleepS
		beds = append(beds, fromNoon(start))
		wakes = append(wakes, fromMidnight(end))
	}
	if len(t.Nights) > 0 {
		t.AvgAsleepS = asleep / len(t.Nights)
	}
	if withNeed > 0 {
		t.AvgNeedS = need / withNeed
	}
	t.Bed = clock(beds, 12*60)
	t.Wake = clock(wakes, 0)
	t.Debt = debt(nights, in.Today, loc)
	t.ShortNights = shortNights(nights, in.Recovery)
	t.HeavyDays = heavyDays(nights, in.Load, loc)
	t.Evenings, t.EveningsLogged = evenings(in.Evenings, nights, in.Recovery, in.Workouts, loc)
	t.MinEvening = MinEvening
	return t
}

// fromNoon is minutes since the noon before: bedtimes either side of
// midnight stay close (23:30 is 690, 00:30 is 750).
func fromNoon(t time.Time) int {
	m := t.Hour()*60 + t.Minute() - 12*60
	if m < 0 {
		m += 24 * 60
	}
	return m
}

func fromMidnight(t time.Time) int { return t.Hour()*60 + t.Minute() }

// clock is the median of minutes counted from offset, and the median
// distance from it.
func clock(ms []int, offset int) Clock {
	if len(ms) == 0 {
		return Clock{}
	}
	f := make([]float64, len(ms))
	for i, m := range ms {
		f[i] = float64(m)
	}
	med := median(f)
	dev := make([]float64, len(ms))
	for i, m := range f {
		dev[i] = math.Abs(m - med)
	}
	at := (int(math.Round(med)) + offset) % (24 * 60)
	return Clock{Median: time.Date(0, 1, 1, at/60, at%60, 0, 0, time.UTC).Format("15:04"),
		SpreadMin: int(math.Round(median(dev))), Nights: len(ms)}
}

// debt sums the shortfall against baseline need of the nights that woke in
// the seven days to upto; a long night pays some back, and the total never
// goes below zero. A gap isn't filled with nights from weeks before.
func debt(nights []store.Sleep, upto string, loc *time.Location) Debt {
	var d Debt
	end, err := time.ParseInLocation("2006-01-02", upto, loc)
	if err != nil {
		return d
	}
	from := end.AddDate(0, 0, -6).Format("2006-01-02")
	for _, x := range nights {
		if x.Date < from || x.Date > upto || x.Need == nil || x.Need.BaselineS == 0 {
			continue
		}
		d.S += x.Need.BaselineS - x.AsleepS
		d.Nights++
	}
	d.S = max(d.S, 0)
	return d
}

func norms(nights []store.Sleep) Norms {
	var rr, deep, rem, eff []float64
	for _, x := range nights {
		if x.RespiratoryRate != nil {
			rr = append(rr, *x.RespiratoryRate)
		}
		if x.DeepS > 0 || x.REMS > 0 {
			deep = append(deep, float64(x.DeepS))
			rem = append(rem, float64(x.REMS))
		}
		if x.Efficiency != nil {
			eff = append(eff, float64(*x.Efficiency))
		}
	}
	n := Norms{Nights: len(nights), DeepS: int(math.Round(median(deep))), REMS: int(math.Round(median(rem)))}
	if len(rr) > 0 {
		v := math.Round(median(rr)*10) / 10
		n.RespiratoryRate = &v
	}
	if len(eff) > 0 {
		v := int(math.Round(median(eff)))
		n.Efficiency = &v
	}
	return n
}

// shortNights compares the next morning's recovery after a night more than
// an hour under baseline need with the other nights. Whoop files a morning's
// recovery under the date the night ended, as sleep is filed.
func shortNights(nights []store.Sleep, rec []store.Recovery) Compare {
	score := map[string]float64{}
	for _, r := range rec {
		score[r.Date] = float64(r.Score)
	}
	var with, without []float64
	for _, x := range nights {
		s, ok := score[x.Date]
		if !ok || x.Need == nil || x.Need.BaselineS == 0 {
			continue
		}
		if x.Need.BaselineS-x.AsleepS > ShortNightS {
			with = append(with, s)
		} else {
			without = append(without, s)
		}
	}
	return compare(with, without, 0)
}

// heavyDays compares hours asleep the night after a heavy training day
// (load at least 1.5 times fitness) with the nights after other days.
func heavyDays(nights []store.Sleep, days []load.Day, loc *time.Location) Compare {
	heavy := map[string]bool{} // keyed by the next day: the night's wake-up date
	known := map[string]bool{}
	for _, d := range days {
		t, err := time.ParseInLocation("2006-01-02", d.Date, loc)
		if err != nil || d.CTL <= 5 { // no history yet: nothing is heavy against it
			continue
		}
		next := t.AddDate(0, 0, 1).Format("2006-01-02")
		known[next] = true
		heavy[next] = d.TSS >= HeavyDayLoad*d.CTL
	}
	var with, without []float64
	for _, x := range nights {
		if !known[x.Date] {
			continue
		}
		h := float64(x.AsleepS) / 3600
		if heavy[x.Date] {
			with = append(with, h)
		} else {
			without = append(without, h)
		}
	}
	return compare(with, without, 1)
}

func compare(with, without []float64, decimals int) Compare {
	p := math.Pow(10, float64(decimals))
	side := func(v []float64) Side { return Side{Nights: len(v), Median: math.Round(median(v)*p) / p} }
	c := Compare{With: side(with), Without: side(without)}
	c.Ready = c.With.Nights >= MinCompare && c.Without.Nights >= MinCompare
	return c
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	m := len(s) / 2
	if len(s)%2 == 1 {
		return s[m]
	}
	return (s[m-1] + s[m]) / 2
}
