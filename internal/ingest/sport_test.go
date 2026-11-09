package ingest_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"testing"
	"time"

	"github.com/tormoder/fit"

	"domestique/internal/activity"
	"domestique/internal/ingest"
)

// noGPS re-encodes a FIT ride with its positions removed, like a head unit
// whose GPS never locked. The altimeter and everything else stay.
func noGPS(t *testing.T, data []byte) []byte {
	t.Helper()
	f, err := fit.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	act, err := f.Activity()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range act.Records {
		r.PositionLat, r.PositionLong = fit.NewLatitudeInvalid(), fit.NewLongitudeInvalid()
	}
	for _, s := range act.Sessions {
		s.StartPositionLat, s.StartPositionLong = fit.NewLatitudeInvalid(), fit.NewLongitudeInvalid()
	}
	for _, l := range act.Laps {
		l.StartPositionLat, l.StartPositionLong = fit.NewLatitudeInvalid(), fit.NewLongitudeInvalid()
		l.EndPositionLat, l.EndPositionLong = fit.NewLatitudeInvalid(), fit.NewLongitudeInvalid()
	}
	var b bytes.Buffer
	if err := fit.Encode(&b, f, binary.LittleEndian); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// Whoop never records GPS, so it can't tell an outdoor ride from an indoor
// one. Paired with a bike computer or Zwift, the other recording decides,
// whichever lands first; alone, Whoop's ride reads as indoor.
func TestWhoopHasNoSayIndoorOrOutdoor(t *testing.T) {
	outdoor := fixture(t, "dcrainmaker/Edge810-Vector-2013-08-16-15-35-10.fit")
	lost := noGPS(t, outdoor)
	indoor := fixture(t, "python-fitparse/sample-activity-indoor-trainer.fit")
	for _, c := range []struct {
		name       string
		file       []byte // the bike computer's recording; nil for Whoop alone
		whoopSport string
		want       string
	}{
		{"outdoor ride, Whoop cycling", outdoor, "Ride", activity.SportRide},
		{"outdoor ride, Whoop spin", outdoor, "VirtualRide", activity.SportRide},
		{"outdoor ride, no GPS lock, Whoop cycling", lost, "Ride", activity.SportRide},
		{"trainer ride, Whoop cycling", indoor, "Ride", activity.SportVirtualRide},
		{"Whoop cycling alone", nil, "Ride", activity.SportVirtualRide},
	} {
		for _, whoopFirst := range []bool{false, true} {
			in, st, _ := setup(t)
			ctx := context.Background()
			start, elapsed := time.Date(2026, 10, 3, 18, 0, 0, 0, time.UTC), 3600
			if c.file != nil {
				p, err := activity.ParseFIT(c.file)
				if err != nil {
					t.Fatal(err)
				}
				start, elapsed = p.Start, p.ElapsedS
			}
			whoop := func() ingest.Result {
				strain := 12.0
				// Whoop notices a few minutes in and stops a little early.
				res, err := in.AddWhoopWorkout(ctx, "w1", start.Add(3*time.Minute), elapsed-300, 0,
					ingest.Hint{Source: "whoop", Sport: c.whoopSport, Strain: &strain}, 140, 170, [6]int64{})
				if err != nil {
					t.Fatal(err)
				}
				return res
			}
			computer := func() ingest.Result {
				res, err := in.Ingest(ctx, "ride.fit", c.file, "wahoo-api", ingest.Hint{Source: "wahoo"})
				if err != nil {
					t.Fatal(err)
				}
				return res
			}
			var res ingest.Result
			switch {
			case c.file == nil:
				res = whoop()
			case whoopFirst:
				whoop()
				res = computer()
			default:
				computer()
				res = whoop()
			}
			a, err := st.GetActivity(ctx, res.ActivityID)
			if err != nil {
				t.Fatal(err)
			}
			if c.file != nil {
				if srcs, _ := st.SourcesFor(ctx, a.ID); len(srcs) != 2 {
					t.Fatalf("%s (whoopFirst=%v): %d sources, want the two merged", c.name, whoopFirst, len(srcs))
				}
			}
			if a.Sport != c.want {
				t.Errorf("%s (whoopFirst=%v): sport %q, want %q", c.name, whoopFirst, a.Sport, c.want)
			}
		}
	}
}

// Zwift says indoor and Whoop has no say, so a Zwift ride stays indoor.
func TestZwiftWithWhoopStaysIndoor(t *testing.T) {
	in, st, _ := setup(t)
	ctx := context.Background()
	start := time.Date(2026, 10, 1, 17, 46, 0, 0, time.UTC)
	putRecording(t, st, "a_ride", "zwift:1", "zwift", "virtual_ride", start, 60, nil)
	strain := 11.2
	if _, err := in.AddWhoopWorkout(ctx, "w1", start.Add(6*time.Minute), 50*60, 0,
		ingest.Hint{Source: "whoop", Sport: "Ride", Strain: &strain}, 140, 165, [6]int64{}); err != nil {
		t.Fatal(err)
	}
	if a, _ := st.GetActivity(ctx, "a_ride"); a.Sport != activity.SportVirtualRide {
		t.Errorf("Zwift + Whoop sport %q, want indoor", a.Sport)
	}
}

// Remerge heals rides an older rule turned indoor.
func TestRemergeHealsWhoopIndoor(t *testing.T) {
	ride := fixture(t, "dcrainmaker/Edge810-Vector-2013-08-16-15-35-10.fit")
	p, _ := activity.ParseFIT(ride)
	in, st, _ := setup(t)
	ctx := context.Background()
	res, err := in.Ingest(ctx, "ride.fit", ride, "wahoo-api", ingest.Hint{Source: "wahoo"})
	if err != nil {
		t.Fatal(err)
	}
	strain := 12.0
	if _, err := in.AddWhoopWorkout(ctx, "w1", p.Start.Add(3*time.Minute), p.ElapsedS-300, 0,
		ingest.Hint{Source: "whoop", Sport: "Ride", Strain: &strain}, 140, 170, [6]int64{}); err != nil {
		t.Fatal(err)
	}
	// What the old rule stored.
	a, _ := st.GetActivity(ctx, res.ActivityID)
	a.Sport = activity.SportVirtualRide
	if err := st.PutActivity(ctx, a); err != nil {
		t.Fatal(err)
	}
	if _, err := in.Remerge(ctx); err != nil {
		t.Fatal(err)
	}
	if a, _ = st.GetActivity(ctx, res.ActivityID); a.Sport != activity.SportRide {
		t.Errorf("after remerge sport %q, want %q", a.Sport, activity.SportRide)
	}
}

// A head unit's outdoor ride without GPS, stored as indoor by the old rule,
// is re-read from the archived file and comes back outdoor.
func TestRemergeResportsNoGPSRide(t *testing.T) {
	ride := noGPS(t, fixture(t, "dcrainmaker/Edge810-Vector-2013-08-16-15-35-10.fit"))
	p, err := activity.ParseFIT(ride)
	if err != nil {
		t.Fatal(err)
	}
	if p.Stream.HasGPS() || p.Sport != activity.SportRide {
		t.Fatalf("stripped fixture: gps=%v sport=%q, want no GPS and outdoor", p.Stream.HasGPS(), p.Sport)
	}
	in, st, _ := setup(t)
	ctx := context.Background()
	res, err := in.Ingest(ctx, "ride.fit", ride, "wahoo-api", ingest.Hint{Source: "wahoo"})
	if err != nil {
		t.Fatal(err)
	}
	// What the old rule stored.
	src, _ := st.GetSource(ctx, res.SourceID)
	src.Sport = activity.SportVirtualRide
	a, _ := st.GetActivity(ctx, res.ActivityID)
	a.Sport = activity.SportVirtualRide
	for _, err := range []error{st.PutSource(ctx, src), st.PutActivity(ctx, a)} {
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := in.Remerge(ctx); err != nil {
		t.Fatal(err)
	}
	for _, got := range []func() (string, error){
		func() (string, error) { s, err := st.GetSource(ctx, res.SourceID); return s.Sport, err },
		func() (string, error) { a, err := st.GetActivity(ctx, res.ActivityID); return a.Sport, err },
	} {
		if sp, err := got(); err != nil || sp != activity.SportRide {
			t.Errorf("after remerge sport %q (%v), want %q", sp, err, activity.SportRide)
		}
	}
}

// A plain "Ride" label (a Strava export) doesn't turn a GPS-less file that
// climbs back indoor.
func TestRideLabelKeepsClimbingFileOutdoor(t *testing.T) {
	ride := noGPS(t, fixture(t, "dcrainmaker/Edge810-Vector-2013-08-16-15-35-10.fit"))
	in, st, _ := setup(t)
	ctx := context.Background()
	res, err := in.Ingest(ctx, "ride.fit", ride, "strava-export", ingest.Hint{Source: "strava", Sport: "Ride"})
	if err != nil {
		t.Fatal(err)
	}
	if a, _ := st.GetActivity(ctx, res.ActivityID); a.Sport != activity.SportRide {
		t.Errorf("sport %q, want %q", a.Sport, activity.SportRide)
	}
}
