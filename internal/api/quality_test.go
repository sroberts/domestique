package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"domestique/internal/activity"
	"domestique/internal/ingest"
	"domestique/internal/store"
	"domestique/internal/sweep"
)

// A channel System 1 left out shows on the activity, and "It's fine" puts
// it back and stops it being asked about again.
func TestBadDataAndRideType(t *testing.T) {
	a, st, h := testAPI(t)
	ctx := context.Background()
	a.Ingest = &ingest.Ingester{Store: st, StorageDir: t.TempDir(), Log: slog.New(slog.DiscardHandler), Loc: time.UTC}
	at := time.Date(2026, 10, 6, 8, 0, 0, 0, time.UTC).UnixMilli()
	sha := "sha-1"
	if err := st.PutActivity(ctx, store.Activity{ID: "a1", Sport: "ride", Name: "Morning Ride", StartAt: at,
		ElapsedS: 600, MovingS: 600, Metrics: []byte("{}")}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutSource(ctx, store.Source{ID: "wahoo:1", ActivityID: "a1", Source: "wahoo", Sport: "ride",
		StartAt: at, ElapsedS: 600, MovingS: 600, HasHR: true, RawSHA256: &sha, ImportedAt: at,
		Extra: []byte(`{"ai_bad": {"hr": "strap dropouts"}}`)}); err != nil {
		t.Fatal(err)
	}
	var s activity.Stream
	for i := range 600 {
		s.T = append(s.T, int32(i))
		s.HR = append(s.HR, int32(130+i%7))
	}
	if err := st.PutStream(ctx, "wahoo:1", &s); err != nil {
		t.Fatal(err)
	}
	if err := a.Ingest.Rebuild(ctx, "a1"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSyncState(ctx, sweep.RideTypePrefix+"a1", "tempo"); err != nil {
		t.Fatal(err)
	}

	var d struct {
		Metrics  activity.Metrics  `json:"metrics"`
		BadData  map[string]string `json:"bad_data"`
		RideType string            `json:"ride_type"`
	}
	w := call(t, h, http.MethodGet, "/v1/activities/a1", "")
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil || w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if d.BadData["hr"] != "strap dropouts" || d.RideType != "tempo" || d.Metrics.AvgHR != 0 {
		t.Fatalf("detail: bad %v, type %q, avg HR %d", d.BadData, d.RideType, d.Metrics.AvgHR)
	}

	if w := call(t, h, http.MethodPost, "/v1/activities/a1/data-ok", ""); w.Code != 200 {
		t.Fatalf("data-ok: %d %s", w.Code, w.Body)
	}
	d.BadData = nil
	w = call(t, h, http.MethodGet, "/v1/activities/a1", "")
	_ = json.Unmarshal(w.Body.Bytes(), &d)
	if len(d.BadData) != 0 || d.Metrics.AvgHR == 0 {
		t.Fatalf("after data-ok: bad %v, avg HR %d", d.BadData, d.Metrics.AvgHR)
	}
	if v, _ := st.SyncState(ctx, sweep.BadKeptPrefix+"wahoo:1"); v != "kept" {
		t.Fatalf("kept = %q", v)
	}
	if w := call(t, h, http.MethodPost, "/v1/activities/nope/data-ok", ""); w.Code != 404 {
		t.Fatalf("unknown activity: %d", w.Code)
	}

	// The week's facts carry its ride minutes by type.
	f, err := a.weekFacts(ctx, time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC))
	if err != nil || f.RideMix["tempo"] != 10 {
		t.Fatalf("ride mix = %v, err %v", f.RideMix, err)
	}
}
