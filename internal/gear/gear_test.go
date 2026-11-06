package gear

import (
	"testing"
	"time"

	"domestique/internal/store"
)

func ms(y int, m time.Month, d int) int64 {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC).UnixMilli()
}
func ptr[T any](v T) *T { return &v }

var (
	road    = store.Bike{ID: "road", Name: "Tarmac", Aliases: []string{"Specialized Tarmac SL7"}, DefaultRide: true}
	trainer = store.Bike{ID: "trainer", Name: "Trainer bike", DefaultVirtual: true}
	old     = store.Bike{ID: "old", Name: "Bianchi", Retired: true}
)

func TestAssignPrecedence(t *testing.T) {
	bikes := []store.Bike{road, trainer, old}
	over := map[string]*string{"m1": ptr("old"), "m2": nil, "m3": ptr("deleted")}
	for _, c := range []struct {
		ride     store.GearActivity
		bike, by string
	}{
		// 1. manual beats Strava's gear and the default
		{store.GearActivity{ID: "m1", Sport: "ride", StravaGear: "Tarmac"}, "old", ByManual},
		// manual "no bike"
		{store.GearActivity{ID: "m2", Sport: "ride"}, "", ByManual},
		// a manual choice of a deleted bike falls back to automatic
		{store.GearActivity{ID: "m3", Sport: "ride"}, "road", ByDefault},
		// 2. Strava gear by alias, case and spacing ignored, beats the default
		{store.GearActivity{ID: "s1", Sport: "virtual_ride", StravaGear: "  specialized   TARMAC sl7 "}, "road", ByStrava},
		// Strava gear can name a retired bike
		{store.GearActivity{ID: "s2", Sport: "ride", StravaGear: "bianchi"}, "old", ByStrava},
		// Strava gear naming an unknown bike: that ride was on another bike
		{store.GearActivity{ID: "s3", Sport: "ride", StravaGear: "Hire bike"}, "", ByNone},
		// 3. default per kind of ride
		{store.GearActivity{ID: "d1", Sport: "ride"}, "road", ByDefault},
		{store.GearActivity{ID: "d2", Sport: "virtual_ride"}, "trainer", ByDefault},
	} {
		got := Assign(c.ride, bikes, over)
		if got.BikeID != c.bike || got.How != c.by {
			t.Errorf("%s: got %+v, want %s by %s", c.ride.ID, got, c.bike, c.by)
		}
	}
	// No default for indoor rides: unassigned.
	if a := Assign(store.GearActivity{ID: "x", Sport: "virtual_ride"}, []store.Bike{road}, nil); a.BikeID != "" {
		t.Errorf("indoor ride without an indoor default went to %q", a.BikeID)
	}
	// A retired bike is nobody's default.
	r := road
	r.Retired = true
	if a := Assign(store.GearActivity{ID: "x", Sport: "ride"}, []store.Bike{r}, nil); a.BikeID != "" {
		t.Errorf("retired default took a ride")
	}
}

func TestMileage(t *testing.T) {
	bike := road
	bike.StartDistanceM = 1_000_000 // 1,000 km before Domestique
	rides := []store.GearActivity{
		{ID: "r1", Sport: "ride", StartAt: ms(2026, 1, 10), DistanceM: 100_000, MovingS: 3600},
		{ID: "r2", Sport: "ride", StartAt: ms(2026, 3, 1) + 3600_000, DistanceM: 50_000, MovingS: 1800}, // morning of the swap
		{ID: "r3", Sport: "ride", StartAt: ms(2026, 6, 1), DistanceM: 2_900_000, MovingS: 36000},
		{ID: "r4", Sport: "virtual_ride", StartAt: ms(2026, 6, 2), DistanceM: 30_000, MovingS: 3600}, // trainer bike
		{ID: "r5", Sport: "ride", StartAt: ms(2026, 6, 3), DistanceM: 40_000, MovingS: 3600, StravaGear: "Hire bike"},
	}
	swap := ms(2026, 3, 1) + 7200_000 // chain replaced after r2
	comps := []store.Component{
		{ID: "chain1", BikeID: "road", Kind: "chain", InstalledAt: ms(2025, 12, 1), RemovedAt: &swap, ReplaceAtM: 3_000_000, ReplacedBy: ptr("chain2")},
		{ID: "chain2", BikeID: "road", Kind: "chain", InstalledAt: swap, ReplaceAtM: 3_000_000, StartDistanceM: 200_000},
		{ID: "tyres", BikeID: "road", Kind: "tyres", Name: "GP5000", InstalledAt: ms(2026, 1, 1), ReplaceAtM: 4_000_000},
		{ID: "pads", BikeID: "road", Kind: "brake_pads", InstalledAt: ms(2026, 1, 1)}, // no target
	}
	v := Compute(Input{Bikes: []store.Bike{bike, trainer}, Components: comps, Rides: rides})

	var b BikeView
	for _, x := range v.Bikes {
		if x.ID == "road" {
			b = x
		}
	}
	if b.DistanceM != 1_000_000+100_000+50_000+2_900_000 || b.Rides != 3 || b.MovingS != 3600+1800+36000 {
		t.Fatalf("bike: %v m over %d rides, %d s", b.DistanceM, b.Rides, b.MovingS)
	}
	if v.Bikes[0].ID != "road" || b.Status != Due {
		t.Fatalf("the bike with a due part should come first and be due: %s %s", v.Bikes[0].ID, b.Status)
	}
	if len(b.Components) != 3 || len(b.History) != 1 {
		t.Fatalf("fitted %d, history %d", len(b.Components), len(b.History))
	}
	byID := map[string]ComponentView{}
	for _, c := range append(b.Components, b.History...) {
		byID[c.ID] = c
	}
	// Old chain: r1 and r2 (before the swap). New chain: 200 km start + r3.
	if c := byID["chain1"]; c.DistanceM != 150_000 || c.Rides != 2 {
		t.Errorf("old chain %v m, %d rides", c.DistanceM, c.Rides)
	}
	if c := byID["chain2"]; c.DistanceM != 3_100_000 || c.Status != Due || *c.Wear < 1.03 {
		t.Errorf("new chain %v m, %s", c.DistanceM, c.Status)
	}
	if c := byID["tyres"]; c.DistanceM != 3_050_000 || c.Status != OK || c.Label != "GP5000" {
		t.Errorf("tyres %+v", c)
	}
	if c := byID["pads"]; c.Wear != nil || c.Status != OK || c.Label != "Brake pads" {
		t.Errorf("pads %+v", c)
	}
	if b.Components[0].ID != "chain2" {
		t.Errorf("due part should sort first, got %s", b.Components[0].ID)
	}
	if len(v.Reminders) != 1 || v.Reminders[0].Text != "Chain on Tarmac: 3,100 of 3,000 km. Replace" {
		t.Errorf("reminders %+v", v.Reminders)
	}
	if v.Unassigned.Rides != 1 || len(v.UnmatchedGear) != 1 || v.UnmatchedGear[0].Name != "Hire bike" {
		t.Errorf("unassigned %+v unmatched %+v", v.Unassigned, v.UnmatchedGear)
	}

	// Reassigning recomputes: move r3 to the trainer bike by hand.
	v = Compute(Input{Bikes: []store.Bike{bike, trainer}, Components: comps, Rides: rides,
		Overrides: map[string]*string{"r3": ptr("trainer")}})
	for _, x := range v.Bikes {
		if x.ID == "road" && (x.Status != OK || x.DistanceM != 1_150_000) {
			t.Errorf("after reassigning: road %s %v", x.Status, x.DistanceM)
		}
	}
	if len(v.Reminders) != 0 {
		t.Errorf("reminders after reassigning: %+v", v.Reminders)
	}
}

func TestSensorBattery(t *testing.T) {
	uses := []store.HRUse{
		{ActivityID: "a", StartAt: ms(2026, 1, 1), Seconds: 3600 * 100, Kinds: []string{"wahoo"}, Devices: []string{"ELEMNT BOLT"}},
		{ActivityID: "b", StartAt: ms(2026, 2, 1), Seconds: 3600 * 150, Kinds: []string{"wahoo"}, Devices: []string{"ELEMNT ROAM"}},
		{ActivityID: "c", StartAt: ms(2026, 3, 1), Seconds: 3600 * 140, Kinds: []string{"garmin"}, Devices: []string{"Forerunner 965"}},
	}
	sensors := []store.Sensor{
		{ID: "any", Name: "Chest strap", BatteryAt: ms(2026, 1, 15), BatteryLifeH: 300},                    // b + c = 290 h: soon
		{ID: "elemnt", Name: "TICKR", DeviceMatch: "elemnt", BatteryAt: ms(2025, 1, 1), BatteryLifeH: 200}, // a + b = 250 h: due
		{ID: "fresh", Name: "HRM-Pro", DeviceMatch: "garmin", BatteryAt: ms(2026, 3, 2), BatteryLifeH: 400},
	}
	v := Compute(Input{Sensors: sensors, HR: uses})
	got := map[string]SensorView{}
	for _, s := range v.Sensors {
		got[s.ID] = s
	}
	if s := got["any"]; s.UsedS != 290*3600 || s.Status != Soon {
		t.Errorf("any: %d s %s", s.UsedS, s.Status)
	}
	if s := got["elemnt"]; s.UsedS != 250*3600 || s.Status != Due || s.Activities != 2 {
		t.Errorf("elemnt: %d s %s", s.UsedS, s.Status)
	}
	if s := got["fresh"]; s.UsedS != 0 || s.Status != OK {
		t.Errorf("fresh: %+v", s)
	}
	if v.Sensors[0].ID != "elemnt" || len(v.Reminders) != 2 || v.Reminders[0].Text != "TICKR battery: 250 of 200 h. Replace" {
		t.Errorf("order %s, reminders %+v", v.Sensors[0].ID, v.Reminders)
	}
}

func TestThousands(t *testing.T) {
	for in, want := range map[float64]string{0: "0", 999: "999", 1000: "1,000", 3120.4: "3,120", 1234567: "1,234,567"} {
		if got := Thousands(in); got != want {
			t.Errorf("%v: %q", in, got)
		}
	}
}
