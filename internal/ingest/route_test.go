package ingest_test

import (
	"context"
	"testing"
	"time"

	"domestique/internal/ingest"
)

func TestRouteFromDescription(t *testing.T) {
	for desc, want := range map[string]string{
		"🗺️ Tempus Fugit in Watopia":                                     "Tempus Fugit in Watopia",
		"🗺️ Two Bridges Loop in Watopia\n⚡️ New 5sec power best (90day)": "Two Bridges Loop in Watopia",
		"Felt slow.\n🗺 Cobbled Climbs in Richmond":                       "Cobbled Climbs in Richmond", // no variation selector
		"🗺️  in Watopia":                                                 "",                           // Zwift named no route
		"Soggy, but a milkshake still tastes good after 26 miles.":       "",
		"": "",
	} {
		if got := ingest.RouteFromDescription(desc); got != want {
			t.Errorf("%q: got %q, want %q", desc, got, want)
		}
	}
}

// A ride imported before routes were read from descriptions gets its route
// on backfill; an import from now on gets it straight away.
func TestRouteBackfill(t *testing.T) {
	in, st, _ := setup(t)
	ctx := context.Background()
	start := time.Date(2026, 10, 2, 18, 0, 0, 0, time.UTC)
	putRecording(t, st, "a_old", "strava:1", "strava", "virtual_ride", start, 60, nil)
	if err := st.SetSourceExtra(ctx, "strava:1", "description", "🗺️ Mountain Mash in Watopia"); err != nil {
		t.Fatal(err)
	}
	n, err := in.BackfillRoutes(ctx)
	if err != nil || n != 1 {
		t.Fatalf("backfill: n=%d err=%v", n, err)
	}
	if a, _ := st.GetActivity(ctx, "a_old"); a.Route != "Mountain Mash in Watopia" {
		t.Fatalf("route after backfill = %q", a.Route)
	}
	if n, _ := in.BackfillRoutes(ctx); n != 0 {
		t.Fatalf("second backfill rebuilt %d", n)
	}

	res, err := in.AddManual(ctx, "strava:2", start.Add(48*time.Hour), 3600, 30000,
		ingest.Hint{Source: "strava", Sport: "VirtualRide", Description: "🗺️ Volcano Circuit CCW in Watopia"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if a, _ := st.GetActivity(ctx, res.ActivityID); a.Route != "Volcano Circuit CCW in Watopia" {
		t.Fatalf("route on import = %q", a.Route)
	}
}
