package provider

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"domestique/internal/store"
)

// A night as Whoop's v2 API sends it, every scored field present.
const whoopNight = `{
  "id": "ecfc6a15-4661-442f-a9a4-f160dd7afae8",
  "start": "2026-11-24T23:12:40.000Z",
  "end": "2026-11-25T06:58:10.000Z",
  "nap": false,
  "score_state": "SCORED",
  "score": {
    "stage_summary": {
      "total_in_bed_time_milli": 27930000,
      "total_awake_time_milli": 2400000,
      "total_no_data_time_milli": 0,
      "total_light_sleep_time_milli": 13200000,
      "total_slow_wave_sleep_time_milli": 5940000,
      "total_rem_sleep_time_milli": 6390000,
      "sleep_cycle_count": 4,
      "disturbance_count": 11
    },
    "sleep_needed": {
      "baseline_milli": 27720000,
      "need_from_sleep_debt_milli": 2412000,
      "need_from_recent_strain_milli": 1080000,
      "need_from_recent_nap_milli": -900000
    },
    "respiratory_rate": 15.64,
    "sleep_performance_percentage": 83,
    "sleep_consistency_percentage": 71,
    "sleep_efficiency_percentage": 91.4
  }
}`

func TestSleepOfKeepsEveryField(t *testing.T) {
	var s whoopSleep
	if err := json.Unmarshal([]byte(whoopNight), &s); err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("America/New_York")
	x := sleepOf(s, loc)
	if x.ID != "whoop:ecfc6a15-4661-442f-a9a4-f160dd7afae8" || x.Date != "2026-11-25" || x.Nap {
		t.Fatalf("identity: %+v", x)
	}
	if x.AsleepS != 25530 || x.InBedS != 27930 || x.AwakeS != 2400 || x.LightS != 13200 || x.DeepS != 5940 || x.REMS != 6390 {
		t.Fatalf("stages: %+v", x)
	}
	if *x.Cycles != 4 || *x.Disturbances != 11 || *x.RespiratoryRate != 15.6 {
		t.Fatalf("cycles %d disturbances %d resp %v", *x.Cycles, *x.Disturbances, *x.RespiratoryRate)
	}
	if *x.Performance != 83 || *x.Consistency != 71 || *x.Efficiency != 91 {
		t.Fatalf("percentages: %d %d %d", *x.Performance, *x.Consistency, *x.Efficiency)
	}
	n := x.Need
	if n == nil || n.BaselineS != 27720 || n.DebtS != 2412 || n.StrainS != 1080 || n.NapS != -900 || n.TotalS() != 30312 {
		t.Fatalf("need: %+v", n)
	}
}

// Naps are kept and marked, but a day's night is still its main sleep;
// detail survives the round trip through the store.
func TestSleepNightsAndNaps(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var s whoopSleep
	if err := json.Unmarshal([]byte(whoopNight), &s); err != nil {
		t.Fatal(err)
	}
	night := sleepOf(s, time.UTC)
	nap := night
	nap.ID, nap.Nap, nap.StartAt, nap.EndAt, nap.AsleepS, nap.Need = "whoop:nap", true, night.EndAt+6*3600_000, night.EndAt+7*3600_000, 2400, nil
	for _, x := range []store.Sleep{night, nap} {
		if err := st.PutSleep(ctx, x); err != nil {
			t.Fatal(err)
		}
	}
	nights, err := st.SleepRange(ctx, "2026-11-25", "2026-11-25")
	if err != nil || len(nights) != 1 || nights[0].ID != night.ID {
		t.Fatalf("nights %+v %v, want the one night", nights, err)
	}
	got := nights[0]
	if got.DeepS != 5940 || *got.Efficiency != 91 || *got.RespiratoryRate != 15.6 || got.Need == nil || got.Need.NapS != -900 {
		t.Fatalf("detail lost: %+v", got)
	}
	naps, err := st.NapRange(ctx, "2026-11-25", "2026-11-25")
	if err != nil || len(naps) != 1 || !naps[0].Nap || naps[0].Need != nil {
		t.Fatalf("naps %+v %v", naps, err)
	}
}
