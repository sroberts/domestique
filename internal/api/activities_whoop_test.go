package api

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"domestique/internal/ingest"
	"domestique/internal/load"
	"domestique/internal/store"
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

// A merged workout lists each recording's own numbers, and ?source= shows
// one recording alone.
func TestActivityVersions(t *testing.T) {
	a, h := newTestAPI(t)
	ctx := context.Background()
	a.Ingest = &ingest.Ingester{Store: a.Store, StorageDir: t.TempDir(), Log: a.Log, Loc: time.UTC}
	start := time.Now().Add(-5 * time.Hour).Truncate(time.Second)
	if err := a.Store.PutActivity(ctx, store.Activity{ID: "a_r", Sport: "virtual_ride", Name: "Ride", StartAt: start.UnixMilli(),
		ElapsedS: 3600, MovingS: 3600, Metrics: []byte("{}")}); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.PutSource(ctx, store.Source{ID: "zwift:1", ActivityID: "a_r", Source: "zwift", Sport: "virtual_ride",
		StartAt: start.UnixMilli(), ElapsedS: 3600, MovingS: 3600, DistanceM: 30000, Extra: []byte("{}"), ImportedAt: 1}); err != nil {
		t.Fatal(err)
	}
	strain := 12.5
	res, err := a.Ingest.AddWhoopWorkout(ctx, "w9", start.Add(5*time.Minute), 3000, 0,
		ingest.Hint{Source: "whoop", Sport: "Ride", Strain: &strain}, 148, 171, [6]int64{0, 600000, 1200000, 900000, 300000, 0})
	if err != nil || res.ActivityID != "a_r" {
		t.Fatalf("whoop merge = %+v %v", res, err)
	}

	type detail struct {
		Activity struct {
			StartAt   int64    `json:"start_at"`
			DistanceM float64  `json:"distance_m"`
			Strain    *float64 `json:"strain"`
		} `json:"activity"`
		Metrics struct {
			AvgHR int `json:"avg_hr"`
		} `json:"metrics"`
		HRZonesFrom string `json:"hr_zones_from"`
		View        string `json:"view"`
		Versions    []struct {
			ID     string   `json:"id"`
			Source string   `json:"source"`
			AvgHR  int      `json:"avg_hr"`
			Strain *float64 `json:"strain"`
			TSS    float64  `json:"tss"`
		} `json:"versions"`
	}
	get := func(q string) (int, detail) {
		w := do(t, h, "GET", "/v1/activities/a_r"+q, nil)
		var d detail
		_ = json.Unmarshal(w.Body.Bytes(), &d)
		return w.Code, d
	}
	code, d := get("")
	if code != 200 || len(d.Versions) != 2 || d.View != "" {
		t.Fatalf("combined = %d, %d versions, view %q", code, len(d.Versions), d.View)
	}
	var whoop string
	for _, v := range d.Versions {
		if v.Source == "whoop" {
			whoop = v.ID
			if v.AvgHR != 148 || v.Strain == nil || *v.Strain != 12.5 || v.TSS <= 0 {
				t.Fatalf("whoop version = %+v", v)
			}
		}
	}
	code, d = get("?source=" + whoop)
	if code != 200 || d.View != whoop {
		t.Fatalf("whoop view = %d view %q", code, d.View)
	}
	if d.Activity.StartAt != start.Add(5*time.Minute).UnixMilli() || d.Activity.DistanceM != 0 || d.Metrics.AvgHR != 148 || d.HRZonesFrom != "whoop" {
		t.Fatalf("whoop view shows %+v avg HR %d zones from %q", d.Activity, d.Metrics.AvgHR, d.HRZonesFrom)
	}
	if code, d = get("?source=zwift:1"); code != 200 || d.Activity.DistanceM != 30000 || d.Activity.Strain != nil {
		t.Fatalf("zwift view = %d %+v", code, d.Activity)
	}
	if code, _ = get("?source=nope"); code != 404 {
		t.Fatalf("unknown source = %d, want 404", code)
	}
}
