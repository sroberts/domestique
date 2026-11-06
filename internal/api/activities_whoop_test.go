package api

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"domestique/internal/ingest"
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
