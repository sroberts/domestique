package load

import (
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"

	"domestique/internal/activity"
	"domestique/internal/store"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func ptr[T any](v T) *T { return &v }

// Score picks one method per activity, in order: power when the ride had a
// meter for at least half of it, strain for strength, heart rate, then strain
// for anything else.
func TestScoreMethod(t *testing.T) {
	ath := Athlete{RestHR: 55, LTHR: 171}
	hist := make([]int32, 200)
	hist[150] = 1800 // half an hour at 150 bpm
	hrTSS := activity.HRTSS(hist, ath.RestHR, ath.LTHR)
	powerTSS, powerIF := activity.PowerTSS(200, 3600, 250)

	cases := []struct {
		name       string
		a          store.Activity
		m          activity.Metrics
		wantMethod string
		wantTSS    float64
		wantIF     float64
	}{
		{
			name:       "power with a meter throughout",
			a:          store.Activity{Sport: activity.SportRide, MovingS: 3600, FTP: 250},
			m:          activity.Metrics{NP: 200, PowerSeconds: 3600, HRHist: hist},
			wantMethod: "power", wantTSS: powerTSS, wantIF: powerIF,
		},
		{
			name:       "power for under half the ride falls back to heart rate",
			a:          store.Activity{Sport: activity.SportRide, MovingS: 3600, FTP: 250},
			m:          activity.Metrics{NP: 200, PowerSeconds: 1700, HRHist: hist},
			wantMethod: "hr", wantTSS: hrTSS,
		},
		{
			name:       "power with no FTP yet falls back to heart rate",
			a:          store.Activity{Sport: activity.SportRide, MovingS: 3600},
			m:          activity.Metrics{NP: 200, PowerSeconds: 3600, HRHist: hist},
			wantMethod: "hr", wantTSS: hrTSS,
		},
		{
			name:       "strength scores strain ahead of heart rate",
			a:          store.Activity{Sport: activity.SportStrength, MovingS: 45 * 60, Strain: ptr(16.0)},
			m:          activity.Metrics{HRHist: hist},
			wantMethod: "strain", wantTSS: 75, // capped at an hour at threshold per hour
		},
		{
			name:       "an all-circuit strength session gets the higher cap",
			a:          store.Activity{Sport: activity.SportStrength, MovingS: 45 * 60, Strain: ptr(16.0)},
			m:          activity.Metrics{CircuitShare: 1},
			wantMethod: "strain", wantTSS: 117.2,
		},
		{
			name:       "strength with no strain uses heart rate",
			a:          store.Activity{Sport: activity.SportStrength, MovingS: 45 * 60},
			m:          activity.Metrics{HRHist: hist},
			wantMethod: "hr", wantTSS: hrTSS,
		},
		{
			name:       "other sports with only strain ignore circuit share",
			a:          store.Activity{Sport: activity.SportOther, MovingS: 45 * 60, Strain: ptr(16.0)},
			m:          activity.Metrics{CircuitShare: 1},
			wantMethod: "strain", wantTSS: 75,
		},
		{
			name:       "nothing to score",
			a:          store.Activity{Sport: activity.SportOther, MovingS: 600},
			wantMethod: "none",
		},
		{
			name:       "elapsed time stands in for missing moving time",
			a:          store.Activity{Sport: activity.SportStrength, ElapsedS: 45 * 60, Strain: ptr(16.0)},
			wantMethod: "strain", wantTSS: 75,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tss, ifv, method := Score(c.a, c.m, ath)
			if method != c.wantMethod || tss != c.wantTSS || ifv != c.wantIF {
				t.Fatalf("got %s %.1f IF %.2f, want %s %.1f IF %.2f", method, tss, ifv, c.wantMethod, c.wantTSS, c.wantIF)
			}
		})
	}
}

// Daily runs the 42-day fitness and 7-day fatigue model day by day, splits
// load by sport, and reports form as yesterday's fitness minus fatigue.
func TestDailyFitnessModel(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	day := func(d int, hour int) int64 {
		return time.Date(2026, 3, d, hour, 0, 0, 0, time.UTC).UnixMilli()
	}
	for _, a := range []store.Activity{
		{ID: "ride", Sport: activity.SportRide, StartAt: day(2, 8), MovingS: 3600, TSS: 100},
		{ID: "lift", Sport: activity.SportStrength, StartAt: day(2, 18), MovingS: 2700, TSS: 40},
		{ID: "walk", Sport: activity.SportOther, StartAt: day(4, 12), MovingS: 1800, TSS: 10},
	} {
		if err := st.PutActivity(ctx, a); err != nil {
			t.Fatal(err)
		}
	}

	from := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC)
	days, err := Daily(ctx, st, from, to, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 5 || days[0].Date != "2026-03-01" || days[4].Date != "2026-03-05" {
		t.Fatalf("days %+v", days)
	}

	// Replay the model by hand.
	var ctl, atl float64
	for i, tss := range []float64{0, 140, 0, 10, 0} {
		tsb := ctl - atl
		ctl += (tss - ctl) / 42
		atl += (tss - atl) / 7
		d := days[i]
		if d.TSS != tss || d.CTL != r1(ctl) || d.ATL != r1(atl) || d.TSB != r1(tsb) {
			t.Errorf("%s: got TSS %.1f CTL %.1f ATL %.1f TSB %.1f, want %.1f %.1f %.1f %.1f",
				d.Date, d.TSS, d.CTL, d.ATL, d.TSB, tss, r1(ctl), r1(atl), r1(tsb))
		}
	}
	if d := days[1]; d.RideTSS != 100 || d.StrengthTSS != 40 || d.OtherTSS != 0 || d.Hours != 1.75 {
		t.Errorf("Mar 2 split: %+v", d)
	}
	if d := days[3]; d.OtherTSS != 10 || d.Hours != 0.5 {
		t.Errorf("Mar 4 split: %+v", d)
	}
	// Form on a training day reflects the day before, so it's fresh (zero)
	// on the first one and negative the morning after.
	if days[1].TSB != 0 || days[2].TSB >= 0 {
		t.Errorf("form: Mar 2 %.1f, Mar 3 %.1f", days[1].TSB, days[2].TSB)
	}
}

// Fitness is built from the first activity on, not from the start of the
// requested range, so a short window still shows real fitness.
func TestDailyWarmsUpFromHistory(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	start := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	for i := 0; i < 30; i++ {
		if err := st.PutActivity(ctx, store.Activity{ID: string(rune('a' + i)), Sport: activity.SportRide,
			StartAt: start.AddDate(0, 0, i).UnixMilli(), MovingS: 3600, TSS: 60}); err != nil {
			t.Fatal(err)
		}
	}
	at := time.Date(2026, 1, 30, 0, 0, 0, 0, time.UTC)
	days, err := Daily(ctx, st, at, at, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	// 30 days of 60 TSS: fitness is well on its way to 60, fatigue nearly there.
	want := 60 * (1 - math.Pow(41.0/42, 30))
	if len(days) != 1 || math.Abs(days[0].CTL-want) > 0.1 || days[0].ATL < 58 {
		t.Fatalf("got %+v, want CTL about %.1f", days, want)
	}
}

// Thresholds come from the most deliberate source available: an override,
// then Whoop, then what the data shows, then the configured fallback.
func TestCurrentPrecedence(t *testing.T) {
	ctx := context.Background()
	set := func(t *testing.T, st *store.Store, kv map[string]string) {
		t.Helper()
		for k, v := range kv {
			if err := st.SetAthlete(ctx, k, v); err != nil {
				t.Fatal(err)
			}
		}
	}
	cases := []struct {
		name     string
		kv       map[string]string
		fallback int
		want     Athlete
	}{
		{"nothing known", nil, 0,
			Athlete{FTPSource: "none", MaxHR: 190, MaxHRSource: "default", RestHR: 55, LTHR: 171}},
		{"configured fallback", nil, 200,
			Athlete{FTPSource: "none", MaxHR: 200, MaxHRSource: "default", RestHR: 55, LTHR: 180}},
		{"detected", map[string]string{"ftp_detected": "240", "max_hr_detected": "184", "rest_hr_detected": "48"}, 190,
			Athlete{FTP: 240, FTPSource: "detected", MaxHR: 184, MaxHRSource: "detected", RestHR: 48, LTHR: 166}},
		{"whoop beats detected", map[string]string{"max_hr_detected": "184", "max_hr_whoop": "188"}, 190,
			Athlete{FTPSource: "none", MaxHR: 188, MaxHRSource: "whoop", RestHR: 55, LTHR: 169}},
		{"overrides beat everything", map[string]string{"ftp_detected": "240", "ftp_override": "260",
			"max_hr_whoop": "188", "max_hr_override": "192"}, 190,
			Athlete{FTP: 260, FTPSource: "override", MaxHR: 192, MaxHRSource: "override", RestHR: 55, LTHR: 173}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := openStore(t)
			set(t, st, c.kv)
			if got := Current(ctx, st, c.fallback); got != c.want {
				t.Fatalf("got %+v\nwant %+v", got, c.want)
			}
		})
	}
}

// Recalc derives thresholds from the history (second-highest max HR, median
// resting HR, rolling FTP from 20-minute power) and rescores every activity
// against them.
func TestRecalc(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	now := time.Now()

	curve := func(best20 int) []int {
		c := make([]int, idx20min+1)
		c[idx20min] = best20
		return c
	}
	put := func(a store.Activity, m activity.Metrics) {
		t.Helper()
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		a.Metrics = b
		if err := st.PutActivity(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	// A strap glitch of 230 is dropped: the second-highest max (186) wins.
	put(store.Activity{ID: "r1", Sport: activity.SportRide, StartAt: now.AddDate(0, 0, -20).UnixMilli(), MovingS: 3600},
		activity.Metrics{MaxHR: 230, NP: 220, PowerSeconds: 3600, PowerCurve: curve(260)})
	put(store.Activity{ID: "r2", Sport: activity.SportRide, StartAt: now.AddDate(0, 0, -10).UnixMilli(), MovingS: 3600},
		activity.Metrics{MaxHR: 186, NP: 200, PowerSeconds: 3600, PowerCurve: curve(240)})
	put(store.Activity{ID: "lift", Sport: activity.SportStrength, StartAt: now.AddDate(0, 0, -5).UnixMilli(), MovingS: 45 * 60,
		Strain: ptr(16.0)}, activity.Metrics{MaxHR: 170, CircuitShare: 1})
	for i, rhr := range []int{50, 52, 61} {
		d := now.AddDate(0, 0, -i-1).Format("2006-01-02")
		if err := st.PutRecovery(ctx, store.Recovery{Date: d, Score: 60, RestingHR: rhr, Source: "whoop"}); err != nil {
			t.Fatal(err)
		}
	}

	ath, err := Recalc(ctx, st, 190)
	if err != nil {
		t.Fatal(err)
	}
	if ath.MaxHR != 186 || ath.MaxHRSource != "detected" || ath.RestHR != 52 {
		t.Fatalf("thresholds %+v, want max 186 detected, rest 52", ath)
	}
	// FTP is 95% of the best 20 minutes in the window: 0.95 × 260 = 247.
	if ath.FTP != 247 || ath.FTPSource != "detected" {
		t.Fatalf("FTP %d (%s), want 247 detected", ath.FTP, ath.FTPSource)
	}

	// Each ride is scored against the FTP of its own day: r1 sees only itself.
	r1a, err := st.GetActivity(ctx, "r1")
	if err != nil {
		t.Fatal(err)
	}
	wantTSS, wantIF := activity.PowerTSS(220, 3600, 247)
	if r1a.LoadMethod != "power" || r1a.FTP != 247 || r1a.TSS != wantTSS || r1a.Intensity != wantIF {
		t.Fatalf("r1 scored %+v, want power %.1f", r1a, wantTSS)
	}
	lift, err := st.GetActivity(ctx, "lift")
	if err != nil {
		t.Fatal(err)
	}
	if lift.LoadMethod != "strain" || lift.TSS != 117.2 {
		t.Fatalf("circuit lift scored %s %.1f, want strain 117.2", lift.LoadMethod, lift.TSS)
	}
}
