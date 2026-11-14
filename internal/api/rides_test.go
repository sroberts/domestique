package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"domestique/internal/store"
)

const threshold = `{"name":"Zwift: Over-unders","kind":"indoor","duration_min":90,"intensity":"threshold","tss":95,"notes":"3 x 12 min"}`

func TestRidePlans(t *testing.T) {
	_, _, h := testAPI(t)
	if w := call(t, h, "PUT", "/v1/ride-plans/2026-11-20", threshold); w.Code != 200 {
		t.Fatalf("put: %d %s", w.Code, w.Body)
	}
	// Planning the day again replaces it; names are tidied.
	if w := call(t, h, "PUT", "/v1/ride-plans/2026-11-22", `{"name":"  Endurance,   rolling ","kind":"Outdoor","duration_min":150}`); w.Code != 200 {
		t.Fatalf("put: %d %s", w.Code, w.Body)
	}
	if w := call(t, h, "PUT", "/v1/ride-plans/2026-11-22", `{"name":"Endurance, rolling","kind":"outdoor","duration_min":180,"intensity":"endurance"}`); w.Code != 200 {
		t.Fatalf("replace: %d %s", w.Code, w.Body)
	}
	var got store.RidePlan
	_ = json.Unmarshal(call(t, h, "GET", "/v1/ride-plans/2026-11-22", "").Body.Bytes(), &got)
	if got.Name != "Endurance, rolling" || got.Kind != "outdoor" || got.DurationMin != 180 || got.Intensity != "endurance" {
		t.Fatalf("got %+v", got)
	}
	var list []store.RidePlan
	_ = json.Unmarshal(call(t, h, "GET", "/v1/ride-plans?from=2026-11-19&to=2026-11-30", "").Body.Bytes(), &list)
	if len(list) != 2 || list[0].Date != "2026-11-20" || list[1].Date != "2026-11-22" {
		t.Fatalf("list %+v, want two, soonest first", list)
	}
	_ = json.Unmarshal(call(t, h, "GET", "/v1/ride-plans?from=2026-11-21&to=2026-11-21", "").Body.Bytes(), &list)
	if len(list) != 0 {
		t.Fatalf("list %+v, want none that day", list)
	}
	if w := call(t, h, "DELETE", "/v1/ride-plans/2026-11-20", ""); w.Code != 204 {
		t.Fatalf("delete: %d", w.Code)
	}
	if w := call(t, h, "GET", "/v1/ride-plans/2026-11-20", ""); w.Code != 404 {
		t.Fatalf("after delete: %d", w.Code)
	}
}

func TestRidePlanValidates(t *testing.T) {
	_, _, h := testAPI(t)
	for _, c := range []struct{ path, body, want string }{
		{"/v1/ride-plans/2026-13-01", threshold, "date"},
		{"/v1/ride-plans/2026-11-20", `{"name":"","kind":"indoor","duration_min":60}`, "name"},
		{"/v1/ride-plans/2026-11-20", `{"name":"` + strings.Repeat("x", 81) + `","kind":"indoor","duration_min":60}`, "name"},
		{"/v1/ride-plans/2026-11-20", `{"name":"Ride","kind":"track","duration_min":60}`, "kind"},
		{"/v1/ride-plans/2026-11-20", `{"name":"Ride","kind":"indoor","duration_min":5}`, "duration"},
		{"/v1/ride-plans/2026-11-20", `{"name":"Ride","kind":"indoor","duration_min":60,"tss":-1}`, "tss"},
		{"/v1/ride-plans/2026-11-20", `{"name":"Ride","kind":"indoor","duration_min":60,"intensity":"sweet spot"}`, "intensity"},
	} {
		w := call(t, h, "PUT", c.path, c.body)
		if w.Code != 400 || !strings.Contains(w.Body.String(), c.want) {
			t.Errorf("%s %s: %d %s, want 400 about %s", c.path, c.body, w.Code, w.Body, c.want)
		}
	}
	if w := call(t, h, "GET", "/v1/ride-plans?from=2026-11-20&to=2026-11-19", ""); w.Code != 400 {
		t.Errorf("backwards range: %d", w.Code)
	}
	if w := call(t, h, "GET", "/v1/ride-plans?from=2026-01-01&to=2026-12-31", ""); w.Code != 400 {
		t.Errorf("a year: %d", w.Code)
	}
}

func TestTodayShowsThePlannedRide(t *testing.T) {
	_, st, h := testAPI(t)
	ctx := context.Background()
	get := func() todayResp {
		t.Helper()
		w := call(t, h, "GET", "/v1/today?date=2026-11-03", "")
		if w.Code != 200 {
			t.Fatalf("today: %d %s", w.Code, w.Body)
		}
		var r todayResp
		_ = json.Unmarshal(w.Body.Bytes(), &r)
		return r
	}
	if r := get(); r.Ride != nil {
		t.Fatalf("ride %+v with nothing planned", r.Ride)
	}
	call(t, h, "PUT", "/v1/ride-plans/2026-11-03", threshold)

	// A good morning: ride it as planned, and the call speaks of a plan.
	if err := st.PutRecovery(ctx, store.Recovery{Date: "2026-11-03", Score: 80, Source: "test"}); err != nil {
		t.Fatal(err)
	}
	r := get()
	if r.Ride == nil || r.Ride.Name != "Zwift: Over-unders" || r.Ride.Ease != nil || r.Ride.Done != nil {
		t.Fatalf("ride %+v, want the plan, no ease, not done", r.Ride)
	}
	if r.Readiness.Title == "Good to train" {
		t.Fatalf("readiness %q: a ride is planned", r.Readiness.Title)
	}

	// A red morning: endurance, a third shorter, TSS to match.
	if err := st.PutRecovery(ctx, store.Recovery{Date: "2026-11-03", Score: 20, Source: "test"}); err != nil {
		t.Fatal(err)
	}
	r = get()
	if r.Readiness.Detail != "Recovery is 20%. Keep today's ride to an easy spin in zone 1–2." {
		t.Fatalf("detail %q, want it about the ride: no lift is planned", r.Readiness.Detail)
	}
	e := r.Ride.Ease
	if e == nil || e.Intensity != "endurance" || e.DurationMin != 60 || e.TSS < 40 || e.TSS > 50 || e.Reason == "" {
		t.Fatalf("ease %+v, want 60 min endurance, about 45 TSS", e)
	}

	// A ride that day (the evening one is longer) ticks it off.
	day := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	for i, x := range []struct {
		id    string
		sport string
		hour  int
		secs  int
	}{{"commute", "ride", 8, 1800}, {"zwift", "virtual_ride", 18, 5400}, {"yesterday", "ride", -6, 9000}} {
		if err := st.PutActivity(ctx, store.Activity{ID: x.id, Sport: x.sport, Name: x.id, StartAt: day.Add(time.Duration(x.hour) * time.Hour).UnixMilli(),
			ElapsedS: x.secs, Metrics: []byte("{}"), UpdatedAt: int64(i)}); err != nil {
			t.Fatal(err)
		}
	}
	r = get()
	if r.Ride.Done == nil || r.Ride.Done.ID != "zwift" || r.Ride.Ease != nil {
		t.Fatalf("done %+v ease %+v, want the Zwift ride and no ease", r.Ride.Done, r.Ride.Ease)
	}
}

func TestEaseRide(t *testing.T) {
	plan := func(intensity string, mins, tss int) store.RidePlan {
		return store.RidePlan{Name: "Ride", Kind: "indoor", DurationMin: mins, Intensity: intensity, TSS: tss}
	}
	for _, c := range []struct {
		p     store.RidePlan
		level string
		want  *rideEase
	}{
		{plan("vo2", 60, 0), "push", nil},
		{plan("vo2", 60, 0), "normal", nil},
		{plan("vo2", 60, 0), "hold", &rideEase{Intensity: "threshold", DurationMin: 60}},
		{plan("threshold", 75, 0), "hold", &rideEase{Intensity: "tempo", DurationMin: 75}},
		{plan("tempo", 75, 0), "hold", nil},
		{plan("endurance", 180, 0), "hold", nil},
		{plan("tempo", 90, 0), "easy", &rideEase{Intensity: "endurance", DurationMin: 60}},
		{plan("", 120, 0), "easy", &rideEase{Intensity: "endurance", DurationMin: 80}},
		{plan("recovery", 60, 0), "easy", &rideEase{Intensity: "recovery", DurationMin: 45}},
		{plan("recovery", 40, 0), "easy", nil},
		{plan("endurance", 45, 0), "easy", nil},
		{plan("threshold", 40, 0), "easy", &rideEase{Intensity: "endurance", DurationMin: 40}},
	} {
		got := easeRide(c.p, c.level)
		if (got == nil) != (c.want == nil) || got != nil && (got.Intensity != c.want.Intensity || got.DurationMin != c.want.DurationMin || got.Reason == "") {
			t.Errorf("%s %d min, %s: got %+v, want %+v", c.p.Intensity, c.p.DurationMin, c.level, got, c.want)
		}
	}
	// A target scales with the easier ride: same length, a step down.
	if e := easeRide(plan("vo2", 60, 80), "hold"); e.TSS >= 80 || e.TSS < 70 {
		t.Errorf("tss %d, want a little under 80", e.TSS)
	}
}

// The coach hears about the planned ride, and about recent activities
// (whose start times are epoch ms).
func TestCoachFactsRideAndRecent(t *testing.T) {
	a, _, _ := testAPI(t)
	day := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	resp := todayResp{Date: "2026-11-03",
		Ride: &rideView{RidePlan: store.RidePlan{Name: "Endurance", Kind: "outdoor", DurationMin: 120, Intensity: "endurance"}},
		Latest: []ActivityRow{
			{Activity: store.Activity{Name: "Club run", Sport: "ride", StartAt: day.Add(-15 * time.Hour).UnixMilli(), MovingS: 7200, TSS: 110}},
			{Activity: store.Activity{Name: "Old", Sport: "ride", StartAt: day.AddDate(0, 0, -9).UnixMilli(), MovingS: 3600}},
		}}
	f := a.coachFacts(&resp, day)
	if f.Ride != "Endurance, outdoor, 120 min, endurance" || f.RideDone {
		t.Fatalf("ride fact %q done %v", f.Ride, f.RideDone)
	}
	if len(f.Recent) != 1 || f.Recent[0].When != "yesterday" || f.Recent[0].Min != 120 || f.Recent[0].TSS != 110 {
		t.Fatalf("recent %+v, want yesterday's club run only", f.Recent)
	}
}
