// Package gear computes bike, component and sensor usage from activities.
// Nothing here is stored: totals are derived on every read, so assigning a
// ride to another bike, or back-dating a chain, recomputes everything.
package gear

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"domestique/internal/activity"
	"domestique/internal/store"
)

// Status of a wearing part.
type Status string

const (
	OK   Status = "ok"
	Soon Status = "soon"
	Due  Status = "due"
)

// SoonAt is the share of its life after which a part is "soon".
const SoonAt = 0.9

func rank(s Status) int {
	switch s {
	case Due:
		return 2
	case Soon:
		return 1
	}
	return 0
}

func statusOf(used, target float64) (Status, *float64) {
	if target <= 0 {
		return OK, nil
	}
	w := math.Round(used/target*1000) / 1000
	switch {
	case w >= 1:
		return Due, &w
	case w >= SoonAt:
		return Soon, &w
	}
	return OK, &w
}

// How a ride got its bike.
const (
	ByManual  = "manual"
	ByStrava  = "strava"
	ByDefault = "default"
	ByNone    = "none"
)

// Assignment is the bike a ride counts towards, and why.
type Assignment struct {
	BikeID string `json:"bike_id"` // "" when none
	How    string `json:"how"`
}

func norm(s string) string { return strings.Join(strings.Fields(strings.ToLower(s)), " ") }

// MatchName finds the bike a gear name (from Strava) refers to, by name or
// alias, ignoring case and spacing. Active bikes win over retired ones.
func MatchName(name string, bikes []store.Bike) (string, bool) {
	n := norm(name)
	if n == "" {
		return "", false
	}
	found := ""
	for _, b := range bikes {
		match := norm(b.Name) == n
		for _, a := range b.Aliases {
			match = match || norm(a) == n
		}
		if !match {
			continue
		}
		if !b.Retired {
			return b.ID, true
		}
		if found == "" {
			found = b.ID
		}
	}
	return found, found != ""
}

// Assign picks a ride's bike: a manual choice first, then Strava's gear name
// matched to a bike, then the default bike for the kind of ride. A Strava gear
// name that matches no bike means the ride was on some other bike, so it is
// left unassigned rather than credited to the default.
func Assign(r store.GearActivity, bikes []store.Bike, overrides map[string]*string) Assignment {
	if id, ok := overrides[r.ID]; ok {
		if id == nil {
			return Assignment{How: ByManual}
		}
		for _, b := range bikes {
			if b.ID == *id {
				return Assignment{BikeID: b.ID, How: ByManual}
			}
		}
		// The chosen bike was deleted: fall through to automatic.
	}
	if r.StravaGear != "" {
		if id, ok := MatchName(r.StravaGear, bikes); ok {
			return Assignment{BikeID: id, How: ByStrava}
		}
		return Assignment{How: ByNone}
	}
	for _, b := range bikes {
		if b.Retired {
			continue
		}
		if (r.Sport == activity.SportRide && b.DefaultRide) || (r.Sport == activity.SportVirtualRide && b.DefaultVirtual) {
			return Assignment{BikeID: b.ID, How: ByDefault}
		}
	}
	return Assignment{How: ByNone}
}

// ComponentView is a component with its computed use.
type ComponentView struct {
	store.Component
	Label     string   `json:"label"` // its name, or the kind's ("Chain")
	DistanceM float64  `json:"distance_m"`
	MovingS   int      `json:"moving_s"`
	Rides     int      `json:"rides"`
	Wear      *float64 `json:"wear"` // share of replace-at used; nil with no target
	Status    Status   `json:"status"`
}

// BikeView is a bike with its computed distance and fitted components.
type BikeView struct {
	store.Bike
	DistanceM  float64         `json:"distance_m"`
	MovingS    int             `json:"moving_s"`
	Rides      int             `json:"rides"`
	Status     Status          `json:"status"` // the worst of its components
	Components []ComponentView `json:"components"`
	History    []ComponentView `json:"history"` // removed or replaced, newest first
}

// SensorView is a sensor with its battery use since the last change.
type SensorView struct {
	store.Sensor
	UsedS      int      `json:"used_s"`
	Activities int      `json:"activities"`
	Wear       *float64 `json:"wear"`
	Status     Status   `json:"status"`
}

// Reminder is one item that is due or nearly so.
type Reminder struct {
	Kind   string `json:"kind"` // component | sensor
	ID     string `json:"id"`
	BikeID string `json:"bike_id,omitempty"`
	Status Status `json:"status"`
	// Text reads as a line: "Chain on Tarmac: 3,120 of 3,000 km. Replace".
	Text string `json:"text"`
}

// Unmatched is a Strava gear name no bike answers to.
type Unmatched struct {
	Name      string  `json:"name"`
	Rides     int     `json:"rides"`
	DistanceM float64 `json:"distance_m"`
}

// View is the whole gear picture.
type View struct {
	Bikes     []BikeView   `json:"bikes"`
	Sensors   []SensorView `json:"sensors"`
	Reminders []Reminder   `json:"reminders"`
	// Unassigned rides count towards no bike.
	Unassigned struct {
		Rides     int     `json:"rides"`
		DistanceM float64 `json:"distance_m"`
	} `json:"unassigned"`
	// UnmatchedGear lists Strava gear names that match no bike, most ridden first.
	UnmatchedGear []Unmatched `json:"unmatched_gear"`
}

// KindLabel names a component kind.
func KindLabel(kind string) string {
	switch kind {
	case "chain":
		return "Chain"
	case "tyres":
		return "Tyres"
	case "cassette":
		return "Cassette"
	case "brake_pads":
		return "Brake pads"
	}
	return "Part"
}

// Kinds are the component kinds accepted.
var Kinds = []string{"chain", "tyres", "cassette", "brake_pads", "other"}

// Input is everything Compute reads.
type Input struct {
	Bikes      []store.Bike
	Components []store.Component
	Sensors    []store.Sensor
	Rides      []store.GearActivity
	Overrides  map[string]*string
	HR         []store.HRUse
}

// Compute derives every total.
func Compute(in Input) View {
	v := View{Bikes: []BikeView{}, Sensors: []SensorView{}, Reminders: []Reminder{}, UnmatchedGear: []Unmatched{}}
	idx := map[string]int{}
	for _, b := range in.Bikes {
		idx[b.ID] = len(v.Bikes)
		v.Bikes = append(v.Bikes, BikeView{Bike: b, DistanceM: b.StartDistanceM, Status: OK,
			Components: []ComponentView{}, History: []ComponentView{}})
	}
	// Rides per bike, for the components' windows.
	byBike := map[string][]store.GearActivity{}
	unmatched := map[string]*Unmatched{}
	for _, r := range in.Rides {
		as := Assign(r, in.Bikes, in.Overrides)
		if as.BikeID == "" {
			v.Unassigned.Rides++
			v.Unassigned.DistanceM += r.DistanceM
			if as.How == ByNone && r.StravaGear != "" {
				k := norm(r.StravaGear)
				u := unmatched[k]
				if u == nil {
					u = &Unmatched{Name: strings.TrimSpace(r.StravaGear)}
					unmatched[k] = u
				}
				u.Rides++
				u.DistanceM += r.DistanceM
			}
			continue
		}
		bv := &v.Bikes[idx[as.BikeID]]
		bv.DistanceM += r.DistanceM
		bv.MovingS += moving(r)
		bv.Rides++
		byBike[as.BikeID] = append(byBike[as.BikeID], r)
	}
	for _, u := range unmatched {
		u.DistanceM = math.Round(u.DistanceM)
		v.UnmatchedGear = append(v.UnmatchedGear, *u)
	}
	sort.Slice(v.UnmatchedGear, func(i, j int) bool { return v.UnmatchedGear[i].Rides > v.UnmatchedGear[j].Rides })

	for _, c := range in.Components {
		i, ok := idx[c.BikeID]
		if !ok {
			continue
		}
		cv := ComponentView{Component: c, Label: c.Name, DistanceM: c.StartDistanceM}
		if strings.TrimSpace(cv.Label) == "" {
			cv.Label = KindLabel(c.Kind)
		}
		for _, r := range byBike[c.BikeID] {
			if r.StartAt >= c.InstalledAt && (c.RemovedAt == nil || r.StartAt < *c.RemovedAt) {
				cv.DistanceM += r.DistanceM
				cv.MovingS += moving(r)
				cv.Rides++
			}
		}
		cv.DistanceM = math.Round(cv.DistanceM)
		cv.Status, cv.Wear = statusOf(cv.DistanceM, c.ReplaceAtM)
		bv := &v.Bikes[i]
		if c.RemovedAt != nil {
			cv.Status = OK // history, not a reminder
			bv.History = append(bv.History, cv)
			continue
		}
		bv.Components = append(bv.Components, cv)
	}
	for i := range v.Bikes {
		bv := &v.Bikes[i]
		bv.DistanceM = math.Round(bv.DistanceM)
		sort.SliceStable(bv.Components, func(a, b int) bool {
			ca, cb := bv.Components[a], bv.Components[b]
			if rank(ca.Status) != rank(cb.Status) {
				return rank(ca.Status) > rank(cb.Status)
			}
			return wear(ca.Wear) > wear(cb.Wear)
		})
		sort.SliceStable(bv.History, func(a, b int) bool { return *bv.History[a].RemovedAt > *bv.History[b].RemovedAt })
		for _, c := range bv.Components {
			if rank(c.Status) > rank(bv.Status) {
				bv.Status = c.Status
			}
			if !bv.Retired && c.Status != OK {
				v.Reminders = append(v.Reminders, Reminder{Kind: "component", ID: c.ID, BikeID: bv.ID, Status: c.Status,
					Text: fmt.Sprintf("%s on %s: %s of %s km. %s", c.Label, bv.Name, Thousands(c.DistanceM/1000),
						Thousands(c.ReplaceAtM/1000), action(c.Status))})
			}
		}
	}
	sort.SliceStable(v.Bikes, func(a, b int) bool {
		ba, bb := v.Bikes[a], v.Bikes[b]
		if ba.Retired != bb.Retired {
			return !ba.Retired
		}
		return rank(ba.Status) > rank(bb.Status)
	})

	for _, s := range in.Sensors {
		sv := SensorView{Sensor: s}
		m := strings.ToLower(strings.TrimSpace(s.DeviceMatch))
		for _, u := range in.HR {
			if u.StartAt < s.BatteryAt {
				continue
			}
			if m != "" && !matchesDevice(u, m) {
				continue
			}
			sv.UsedS += u.Seconds
			sv.Activities++
		}
		sv.Status, sv.Wear = statusOf(float64(sv.UsedS)/3600, s.BatteryLifeH)
		if s.Retired {
			sv.Status = OK
		} else if sv.Status != OK {
			v.Reminders = append(v.Reminders, Reminder{Kind: "sensor", ID: s.ID, Status: sv.Status,
				Text: fmt.Sprintf("%s battery: %s of %s h. %s", s.Name, Thousands(float64(sv.UsedS)/3600),
					Thousands(s.BatteryLifeH), action(sv.Status))})
		}
		v.Sensors = append(v.Sensors, sv)
	}
	sort.SliceStable(v.Sensors, func(a, b int) bool {
		sa, sb := v.Sensors[a], v.Sensors[b]
		if sa.Retired != sb.Retired {
			return !sa.Retired
		}
		return rank(sa.Status) > rank(sb.Status)
	})
	sort.SliceStable(v.Reminders, func(a, b int) bool { return rank(v.Reminders[a].Status) > rank(v.Reminders[b].Status) })
	v.Unassigned.DistanceM = math.Round(v.Unassigned.DistanceM)
	return v
}

func matchesDevice(u store.HRUse, m string) bool {
	for _, d := range u.Devices {
		if strings.Contains(strings.ToLower(d), m) {
			return true
		}
	}
	for _, k := range u.Kinds {
		if strings.ToLower(k) == m {
			return true
		}
	}
	return false
}

func action(s Status) string {
	if s == Due {
		return "Replace"
	}
	return "Replace soon"
}

func wear(w *float64) float64 {
	if w == nil {
		return -1
	}
	return *w
}

func moving(r store.GearActivity) int {
	if r.MovingS > 0 {
		return r.MovingS
	}
	return r.ElapsedS
}

// Thousands renders a whole number with comma grouping: 3120 → "3,120".
func Thousands(v float64) string {
	n := int64(math.Round(v))
	neg := n < 0
	if neg {
		n = -n
	}
	s := strconv.FormatInt(n, 10)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}
