package api

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"domestique/internal/ingest"
	"domestique/internal/load"
)

// A workout only Whoop recorded has no stream: the detail must still carry a
// (empty) time list, and Whoop's own zone times.
func TestWhoopOnlyActivityDetail(t *testing.T) {
	a, h := newTestAPI(t)
	a.Ingest = &ingest.Ingester{Store: a.Store, StorageDir: t.TempDir(), Log: a.Log, Loc: time.UTC}
	strain := 7.4
	res, err := a.Ingest.AddWhoopWorkout(context.Background(), "w1", time.Now().Add(-2*time.Hour), 1079, 0,
		ingest.Hint{Source: "whoop", ExternalID: "w1", Sport: "VirtualRide", Strain: &strain}, 142, 155,
		[6]int64{0, 131000, 831000, 117000, 0, 0})
	if err != nil {
		t.Fatal(err)
	}
	w := do(t, h, "GET", "/v1/activities/"+res.ActivityID, nil)
	if w.Code != 200 {
		t.Fatalf("detail = %d %s", w.Code, w.Body)
	}
	var d struct {
		Stream      struct{ T *[]int } `json:"stream"`
		HRZones     [6]int             `json:"hr_zones"`
		HRZonesFrom string             `json:"hr_zones_from"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	if d.Stream.T == nil {
		t.Fatal("stream.t is null")
	}
	if d.HRZones != [6]int{0, 131, 831, 117, 0, 0} || d.HRZonesFrom != "whoop" {
		t.Fatalf("zones = %v from %q", d.HRZones, d.HRZonesFrom)
	}
}

// A Whoop-only workout is scored from Whoop's time in zones, not from strain:
// a short, hard workout with high strain mustn't outscore its own duration.
func TestWhoopOnlyActivityLoad(t *testing.T) {
	a, _ := newTestAPI(t)
	ctx := context.Background()
	a.Ingest = &ingest.Ingester{Store: a.Store, StorageDir: t.TempDir(), Log: a.Log, Loc: time.UTC}
	strain := 14.3
	// 45 minutes, mostly 70-90% of max HR.
	zones := [6]int64{60000, 300000, 600000, 1200000, 540000, 0}
	res, err := a.Ingest.AddWhoopWorkout(ctx, "w2", time.Now().Add(-3*time.Hour), 2700, 0,
		ingest.Hint{Source: "whoop", ExternalID: "w2", Sport: "VirtualRide", Strain: &strain}, 150, 172, zones)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := load.Recalc(ctx, a.Store, 190); err != nil {
		t.Fatal(err)
	}
	act, err := a.Store.GetActivity(ctx, res.ActivityID)
	if err != nil {
		t.Fatal(err)
	}
	if act.LoadMethod != "hr" {
		t.Fatalf("load method = %q, want hr from Whoop zones", act.LoadMethod)
	}
	if act.TSS <= 20 || act.TSS > 75 {
		t.Fatalf("TSS = %v, want a plausible 45-minute score (20-75); strain alone gave 104", act.TSS)
	}
}
