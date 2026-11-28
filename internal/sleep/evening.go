package sleep

import (
	"math"
	"sort"
	"time"

	"domestique/internal/store"
)

// EveningItem is one thing the evening log can hold.
type EveningItem struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

// EveningItems are what the athlete taps, in the order the card shows them.
var EveningItems = []EveningItem{
	{"caffeine", "Caffeine after 2 pm"},
	{"alcohol", "Alcohol"},
	{"late_meal", "Late meal"},
	{"screens", "Screens in bed"},
	{"stressed", "Stressed"},
	{"ill", "Ill"},
}

// LateTraining isn't asked: it's filled in from the workouts, for any
// logged evening with one that ended within LateTrainingS of bedtime.
var LateTraining = EveningItem{"late_training", "Trained late"}

// LateTrainingS is how close to bedtime a workout must end to count as late.
const LateTrainingS = 3 * 3600

// MinEvening is the fewest logged evenings on each side before an item's
// comparison shows: fewer say nothing.
const MinEvening = 8

// IsEveningItem reports whether key is something the athlete can log.
func IsEveningItem(key string) bool {
	for _, it := range EveningItems {
		if it.Key == key {
			return true
		}
	}
	return false
}

// EveningSide is the nights after logged evenings with, or without, an item.
type EveningSide struct {
	Nights     int      `json:"nights"`
	AsleepH    float64  `json:"asleep_h"`
	Efficiency *float64 `json:"efficiency,omitempty"`
	Recovery   *float64 `json:"recovery,omitempty"` // next morning's %
}

// EveningCompare is one item against the other logged evenings.
type EveningCompare struct {
	EveningItem
	With    EveningSide `json:"with"`
	Without EveningSide `json:"without"`
	Ready   bool        `json:"ready"`
}

// Workout is when a workout ended, for late training.
type Workout struct{ EndAt int64 }

// evenings compares each item over logged evenings only: an evening with
// no log is unknown, never counted as "without".
func evenings(logged []store.Evening, nights []store.Sleep, rec []store.Recovery, workouts []Workout, loc *time.Location) ([]EveningCompare, int) {
	byDate := map[string]store.Sleep{}
	for _, n := range nights {
		byDate[n.Date] = n
	}
	score := map[string]float64{}
	for _, r := range rec {
		score[r.Date] = float64(r.Score)
	}
	ends := make([]int64, len(workouts))
	for i, w := range workouts {
		ends[i] = w.EndAt
	}
	sort.Slice(ends, func(i, j int) bool { return ends[i] < ends[j] })

	type evening struct {
		night store.Sleep
		has   map[string]bool
	}
	var evs []evening
	for _, e := range logged {
		d, err := time.ParseInLocation("2006-01-02", e.Date, loc)
		if err != nil {
			continue
		}
		n, ok := byDate[d.AddDate(0, 0, 1).Format("2006-01-02")]
		if !ok {
			continue // no night scored after it yet
		}
		has := map[string]bool{}
		for _, it := range e.Items {
			has[it] = true
		}
		// Any workout ending in the three hours before bed.
		i := sort.Search(len(ends), func(i int) bool { return ends[i] >= n.StartAt-LateTrainingS*1000 })
		has[LateTraining.Key] = i < len(ends) && ends[i] <= n.StartAt
		evs = append(evs, evening{n, has})
	}

	side := func(ns []store.Sleep) EveningSide {
		var asleep, eff, recs []float64
		for _, n := range ns {
			asleep = append(asleep, float64(n.AsleepS)/3600)
			if n.Efficiency != nil {
				eff = append(eff, float64(*n.Efficiency))
			}
			if s, ok := score[n.Date]; ok {
				recs = append(recs, s)
			}
		}
		out := EveningSide{Nights: len(ns), AsleepH: math.Round(median(asleep)*10) / 10}
		if len(eff) > 0 {
			v := math.Round(median(eff))
			out.Efficiency = &v
		}
		if len(recs) > 0 {
			v := math.Round(median(recs))
			out.Recovery = &v
		}
		return out
	}
	out := []EveningCompare{}
	for _, it := range append(append([]EveningItem{}, EveningItems...), LateTraining) {
		var with, without []store.Sleep
		for _, e := range evs {
			if e.has[it.Key] {
				with = append(with, e.night)
			} else {
				without = append(without, e.night)
			}
		}
		c := EveningCompare{EveningItem: it, With: side(with), Without: side(without)}
		c.Ready = c.With.Nights >= MinEvening && c.Without.Nights >= MinEvening
		out = append(out, c)
	}
	return out, len(logged)
}
