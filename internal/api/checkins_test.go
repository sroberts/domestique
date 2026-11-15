package api

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"domestique/internal/store"
)

func putAct(t *testing.T, st *store.Store, id, sport string, at time.Time, secs int, session string) {
	t.Helper()
	a := store.Activity{ID: id, Sport: sport, Name: id, StartAt: at.UnixMilli(), ElapsedS: secs, MovingS: secs, Metrics: []byte("{}")}
	if session != "" {
		a.SessionID = &session
	}
	if err := st.PutActivity(context.Background(), a); err != nil {
		t.Fatal(err)
	}
}

func TestCheckInSession(t *testing.T) {
	_, st, h := testAPI(t)
	ctx := context.Background()
	start := time.Date(2026, 11, 3, 7, 0, 0, 0, time.UTC).UnixMilli()
	fin := start + 50*60_000
	if err := st.PutSession(ctx, store.Session{ID: "lift", TemplateID: "upper", Name: "Upper", StartedAt: start, FinishedAt: &fin, UpdatedAt: fin}); err != nil {
		t.Fatal(err)
	}
	var got checkInResp
	_ = json.Unmarshal(call(t, h, "GET", "/v1/sessions/lift/checkin", "").Body.Bytes(), &got)
	if got.CheckIn != nil || !slices.Equal(got.Questions, []string{"effort", "legs", "plan", "pain"}) || got.Minutes != 50 {
		t.Fatalf("before: %+v", got)
	}
	w := call(t, h, "PUT", "/v1/sessions/lift/checkin", `{"effort":"hard","legs":"heavy","plan":"as_planned","pain":"niggle","where":["shoulder"]}`)
	if w.Code != 200 {
		t.Fatalf("put: %d %s", w.Code, w.Body)
	}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.CheckIn == nil || got.CheckIn.Effort != "hard" || got.CheckIn.WorkoutAt != start || got.SRPE != 6*50 {
		t.Fatalf("after: %+v %+v", got, got.CheckIn)
	}
	for _, bad := range []string{`{"effort":"brutal"}`, `{"pain":"pain","where":["spleen"]}`} {
		if w := call(t, h, "PUT", "/v1/sessions/lift/checkin", bad); w.Code != 400 {
			t.Errorf("%s: %d", bad, w.Code)
		}
	}
	if w := call(t, h, "GET", "/v1/sessions/nope/checkin", ""); w.Code != 404 {
		t.Errorf("unknown session: %d", w.Code)
	}
	// The session's activity, once ingested, shares the check-in.
	putAct(t, st, "act-lift", "strength", time.UnixMilli(start), 3000, "lift")
	_ = json.Unmarshal(call(t, h, "GET", "/v1/activities/act-lift/checkin", "").Body.Bytes(), &got)
	if got.CheckIn == nil || got.CheckIn.Legs != "heavy" {
		t.Fatalf("activity's check-in: %+v", got)
	}
}

func TestCheckInRideQuestions(t *testing.T) {
	_, st, h := testAPI(t)
	day := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	putAct(t, st, "spin", "virtual_ride", day.Add(18*time.Hour), 45*60, "")
	putAct(t, st, "long", "ride", day.AddDate(0, 0, 1).Add(9*time.Hour), 180*60, "")
	call(t, h, "PUT", "/v1/ride-plans/2026-11-03", `{"name":"Spin","kind":"indoor","duration_min":45,"intensity":"recovery"}`)
	call(t, h, "PUT", "/v1/ride-plans/2026-11-04", `{"name":"Long","kind":"outdoor","duration_min":180,"intensity":"endurance"}`)
	q := func(id string) []string {
		var r checkInResp
		_ = json.Unmarshal(call(t, h, "GET", "/v1/activities/"+id+"/checkin", "").Body.Bytes(), &r)
		return r.Questions
	}
	if got := q("spin"); !slices.Equal(got, []string{"effort", "legs"}) {
		t.Errorf("recovery spin: %v", got)
	}
	if got := q("long"); !slices.Equal(got, []string{"effort", "legs", "plan", "pain", "fuel"}) {
		t.Errorf("planned long ride: %v", got)
	}
	if w := call(t, h, "PUT", "/v1/activities/long/checkin", `{"effort":"moderate","fuel":"fuelled_badly"}`); w.Code != 200 {
		t.Fatalf("put: %d %s", w.Code, w.Body)
	}
	if w := call(t, h, "GET", "/v1/activities/missing/checkin", ""); w.Code != 404 {
		t.Errorf("unknown activity: %d", w.Code)
	}
}

func TestTodayAsksAboutOneRecentWorkout(t *testing.T) {
	_, st, h := testAPI(t)
	now := time.Now()
	putAct(t, st, "old", "ride", now.Add(-60*time.Hour), 3600, "")
	putAct(t, st, "run", "run", now.Add(-20*time.Hour), 40*60, "")
	putAct(t, st, "ride", "ride", now.Add(-3*time.Hour), 90*60, "")
	putAct(t, st, "stretch", "other", now.Add(-1*time.Hour), 5*60, "") // too short to ask about
	pending := func() *pendingCheckIn {
		t.Helper()
		w := call(t, h, "GET", "/v1/today?date="+now.Format("2006-01-02"), "")
		if w.Code != 200 {
			t.Fatalf("today: %d %s", w.Code, w.Body)
		}
		var r todayResp
		_ = json.Unmarshal(w.Body.Bytes(), &r)
		return r.CheckIn
	}
	p := pending()
	if p == nil || p.Activity.ID != "ride" || !slices.Contains(p.Questions, "fuel") {
		t.Fatalf("pending %+v, want the ride", p)
	}
	// Skipping it moves on to the run; answering that leaves nothing.
	if w := call(t, h, "PUT", "/v1/activities/ride/checkin", `{"skipped":true}`); w.Code != 200 {
		t.Fatalf("skip: %d %s", w.Code, w.Body)
	}
	if p = pending(); p == nil || p.Activity.ID != "run" {
		t.Fatalf("pending %+v, want the run", p)
	}
	call(t, h, "PUT", "/v1/activities/run/checkin", `{"effort":"easy"}`)
	if p = pending(); p != nil {
		t.Fatalf("pending %+v, want none: the old ride is over two days back", p)
	}
}

func TestCheckInFollowsMerges(t *testing.T) {
	_, st, h := testAPI(t)
	ctx := context.Background()
	at := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	// A Whoop workout answered on Today, later found to be the Wahoo ride.
	putAct(t, st, "whoop", "ride", at, 3600, "")
	putAct(t, st, "wahoo", "ride", at, 3600, "")
	call(t, h, "PUT", "/v1/activities/whoop/checkin", `{"effort":"hard"}`)
	if err := st.MergeActivities(ctx, "whoop", "wahoo"); err != nil {
		t.Fatal(err)
	}
	if c, err := st.GetCheckIn(ctx, "wahoo"); err != nil || c.Effort != "hard" {
		t.Fatalf("after the merge: %+v %v", c, err)
	}
	// Into one that has its own: that one stands.
	putAct(t, st, "dup", "ride", at, 3600, "")
	call(t, h, "PUT", "/v1/activities/dup/checkin", `{"effort":"easy"}`)
	if err := st.MergeActivities(ctx, "dup", "wahoo"); err != nil {
		t.Fatal(err)
	}
	if c, _ := st.GetCheckIn(ctx, "wahoo"); c.Effort != "hard" {
		t.Fatalf("kept %+v", c)
	}
	if _, err := st.GetCheckIn(ctx, "dup"); err == nil {
		t.Fatal("dup's check-in left behind")
	}
	// A Whoop workout answered before its lifting session came in: it joins the session.
	putAct(t, st, "whoop2", "strength", at.Add(5*time.Hour), 3000, "")
	putAct(t, st, "app", "strength", at.Add(5*time.Hour), 3000, "s1")
	call(t, h, "PUT", "/v1/activities/whoop2/checkin", `{"legs":"dead"}`)
	if err := st.MergeActivities(ctx, "whoop2", "app"); err != nil {
		t.Fatal(err)
	}
	if c, err := st.GetCheckIn(ctx, store.SessionKey("s1")); err != nil || c.Legs != "dead" {
		t.Fatalf("session's check-in: %+v %v", c, err)
	}
	// Merging sessions carries it to the kept one.
	start := at.Add(5 * time.Hour).UnixMilli()
	fin := start + 600_000
	for _, id := range []string{"s1", "s2"} {
		if err := st.PutSession(ctx, store.Session{ID: id, TemplateID: "t", Name: "Lift", StartedAt: start, FinishedAt: &fin, UpdatedAt: fin}); err != nil {
			t.Fatal(err)
		}
	}
	keep, _ := st.GetSession(ctx, "s2")
	if err := st.MergeSessions(ctx, keep, "s1", nil); err != nil {
		t.Fatal(err)
	}
	if c, err := st.GetCheckIn(ctx, store.SessionKey("s2")); err != nil || c.Legs != "dead" {
		t.Fatalf("after merging sessions: %+v %v", c, err)
	}
	// And a phone still asking about the merged-away session finds it.
	var got checkInResp
	_ = json.Unmarshal(call(t, h, "GET", "/v1/sessions/s1/checkin", "").Body.Bytes(), &got)
	if got.CheckIn == nil || got.CheckIn.Legs != "dead" {
		t.Fatalf("merged-away session: %+v", got)
	}
}

func TestCheckInsFeedReadinessAndTheCoach(t *testing.T) {
	a, st, h := testAPI(t)
	ctx := context.Background()
	day := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	if err := st.PutRecovery(ctx, store.Recovery{Date: "2026-11-03", Score: 80, Source: "test"}); err != nil {
		t.Fatal(err)
	}
	putAct(t, st, "race", "virtual_ride", day.Add(-6*time.Hour), 3600, "")
	call(t, h, "PUT", "/v1/activities/race/checkin", `{"effort":"all_out","legs":"dead","pain":"niggle","where":["knee"]}`)

	var r todayResp
	_ = json.Unmarshal(call(t, h, "GET", "/v1/today?date=2026-11-03", "").Body.Bytes(), &r)
	if r.Readiness.Level != "hold" || r.Readiness.Title != "Legs still tired" || !strings.Contains(r.Readiness.Detail, "knee niggle") {
		t.Fatalf("readiness %+v", r.Readiness)
	}
	yesterday, _ := st.ListActivities(ctx, store.ActivityQuery{From: day.AddDate(0, 0, -1).UnixMilli(), To: day.UnixMilli() - 1})
	felt, err := a.feltLines(ctx, yesterday)
	if err != nil || len(felt) != 1 || felt[0] != "Mon, race: all out, legs dead, a niggle: knee" {
		t.Fatalf("felt %v %v", felt, err)
	}
	if f := a.coachFacts(&todayResp{felt: felt}, day); len(f.Felt) != 1 {
		t.Fatalf("coach facts felt %v", f.Felt)
	}
	// The day after, it's gone from the call.
	if err := st.PutRecovery(ctx, store.Recovery{Date: "2026-11-04", Score: 80, Source: "test"}); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(call(t, h, "GET", "/v1/today?date=2026-11-04", "").Body.Bytes(), &r)
	if r.Readiness.Level != "normal" {
		t.Fatalf("two days on: %+v", r.Readiness)
	}

	// The ride's debrief hears how it felt.
	d := detailResp{Activity: store.Activity{Name: "race", Sport: "virtual_ride", MovingS: 3600}}
	s, _ := a.activitySubject(ctx, d.Activity)
	s.key = "race"
	c, _ := a.checkInOf(ctx, s)
	d.CheckIn = &c
	if af := a.activityFacts(ctx, &d); af.Felt == nil || af.Felt.Effort != "all out" || af.Felt.Pain != "a niggle: knee" {
		t.Fatalf("activity facts felt %+v", af.Felt)
	}
	// So does the week note.
	mon := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	wf, err := a.weekFacts(ctx, mon)
	if err != nil {
		t.Fatal(err)
	}
	if len(wf.Felt) != 1 || !strings.HasPrefix(wf.Felt[0], "Mon, race: all out") {
		t.Fatalf("week facts felt %v", wf.Felt)
	}
}

func TestCheckInBodyPartsKeepComingUp(t *testing.T) {
	a, st, h := testAPI(t)
	end := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	for i, d := range []int{2, 6, 11} {
		id := "ride" + string(rune('a'+i))
		putAct(t, st, id, "ride", end.AddDate(0, 0, -d), 3600, "")
		call(t, h, "PUT", "/v1/activities/"+id+"/checkin", `{"pain":"niggle","where":["knee","back"]}`)
	}
	got, err := a.complaints(context.Background(), end)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !strings.HasPrefix(got[0], "knee, 3 times") && !strings.HasPrefix(got[1], "knee, 3 times") {
		t.Fatalf("complaints %v, want the knee and the lower back", got)
	}
}
