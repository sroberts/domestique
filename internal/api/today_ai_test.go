package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"domestique/internal/coach"
	"domestique/internal/coach/coachtest"
	"domestique/internal/store"
)

func ts(reps int, kg float64) store.TargetSet { return store.TargetSet{Reps: reps, WeightKg: kg} }
func es(reps int, kg float64) coach.EaseSet   { return coach.EaseSet{Reps: reps, Kg: kg} }

func getToday(t *testing.T, h http.Handler, date string) todayResp {
	t.Helper()
	w := call(t, h, "GET", "/v1/today?date="+date, "")
	if w.Code != 200 {
		t.Fatalf("today: %d %s", w.Code, w.Body)
	}
	var r todayResp
	_ = json.Unmarshal(w.Body.Bytes(), &r)
	return r
}

// On a low-recovery day with a session planned, the LLM's easier version is
// offered; taking it changes that day's plan only, and only ever easier.
func TestEaseProposal(t *testing.T) {
	a, st, h := testAPI(t)
	ctx := context.Background()
	const date = "2026-11-03"
	if err := st.PutRecovery(ctx, store.Recovery{Date: date, Score: 20, Source: "test"}); err != nil {
		t.Fatal(err)
	}
	squat := store.Exercise{Name: "Back Squat", RestSeconds: 180, Sets: []store.TargetSet{ts(5, 100), ts(5, 100), ts(5, 100)}}
	if err := st.PutTemplate(ctx, store.Template{ID: "t1", Name: "Lower", Exercises: []store.Exercise{squat}, UpdatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutPlan(ctx, date, "t1"); err != nil {
		t.Fatal(err)
	}
	reply := `{"reason": "Recovery is 20%, so squat 80 kg today.", "exercises": [{"name": "back squat", "sets": [{"reps": 5, "kg": 80}, {"reps": 5, "kg": 80}]}]}`
	f := &coachtest.Fake{Chat: func(system, _ string) string {
		if strings.Contains(system, "easier version") {
			return reply
		}
		return `{"title": "Ease off", "detail": "Recovery is 20%."}`
	}}
	a.Coach = &coach.Writer{AI: f.AI(t), KV: st, Log: a.Log}

	var r todayResp
	for range 400 {
		if r = getToday(t, h, date); r.Ease != nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if r.Ease == nil || r.Ease.Exercises[0].Name != "Back Squat" || r.Ease.Exercises[0].Sets[0].WeightKg != 80 ||
		r.Ease.Exercises[0].RestSeconds != 180 || r.Ease.Exercises[0].Change != easedNote {
		t.Fatalf("proposal: %+v", r.Ease)
	}
	if r.Eased || len(r.Plan.Exercises[0].Sets) != 2 || r.Plan.Exercises[0].Sets[0].WeightKg != 100 {
		t.Fatalf("plan before taking it: %+v", r.Plan)
	}

	// Anything harder than the day's prescription is refused.
	harder := `{"exercises": [{"name": "Back Squat", "sets": [{"reps": 6, "weight_kg": 100}]}]}`
	if w := call(t, h, "POST", "/v1/plans/"+date+"/ease", harder); w.Code != 400 {
		t.Fatalf("harder: %d %s", w.Code, w.Body)
	}
	body, _ := json.Marshal(map[string]any{"exercises": r.Ease.Exercises})
	if w := call(t, h, "POST", "/v1/plans/"+date+"/ease", string(body)); w.Code != 204 {
		t.Fatalf("take: %d %s", w.Code, w.Body)
	}
	r = getToday(t, h, date)
	if !r.Eased || r.Ease != nil || r.Plan.Exercises[0].Sets[0].WeightKg != 80 || r.Template.Exercises[0].Sets[0].WeightKg != 100 {
		t.Fatalf("after taking it: eased=%v ease=%+v plan=%+v", r.Eased, r.Ease, r.Plan)
	}
	if tpl, _ := st.GetTemplate(ctx, "t1"); tpl.Exercises[0].Sets[0].WeightKg != 100 {
		t.Fatal("template changed")
	}
	// Another day's plan is untouched.
	if err := st.PutPlan(ctx, "2026-11-04", "t1"); err != nil {
		t.Fatal(err)
	}
	if r := getToday(t, h, "2026-11-04"); r.Eased || r.Plan.Exercises[0].Sets[0].WeightKg != 100 {
		t.Fatalf("next day: %+v", r.Plan)
	}

	// Undo brings back the plan and the offer; keeping the plan hides it.
	if w := call(t, h, "DELETE", "/v1/plans/"+date+"/ease", ""); w.Code != 204 {
		t.Fatalf("undo: %d", w.Code)
	}
	if r := getToday(t, h, date); r.Eased || r.Ease == nil {
		t.Fatalf("after undo: eased=%v ease=%v", r.Eased, r.Ease != nil)
	}
	if w := call(t, h, "POST", "/v1/plans/"+date+"/ease", `{"keep": true}`); w.Code != 204 {
		t.Fatalf("keep: %d", w.Code)
	}
	if r := getToday(t, h, date); r.Eased || r.Ease != nil {
		t.Fatalf("after keep: eased=%v ease=%v", r.Eased, r.Ease != nil)
	}
}

// A reply that adds weight is no easier session, and isn't offered.
func TestCheckEase(t *testing.T) {
	plan := []coach.EaseExercise{{Name: "Bench", Sets: []coach.EaseSet{es(8, 60), es(8, 60)}}, {Name: "Pull-up", Sets: []coach.EaseSet{es(6, 0)}}}
	for name, got := range map[string][]coach.EaseExercise{
		"heavier":    {{Name: "Bench", Sets: []coach.EaseSet{es(8, 62.5)}}, {Name: "Pull-up", Sets: []coach.EaseSet{es(6, 0)}}},
		"more sets":  {{Name: "Bench", Sets: []coach.EaseSet{es(8, 60), es(8, 60), es(8, 50)}}, {Name: "Pull-up", Sets: []coach.EaseSet{es(6, 0)}}},
		"renamed":    {{Name: "Dips", Sets: []coach.EaseSet{es(8, 60)}}, {Name: "Pull-up", Sets: []coach.EaseSet{es(6, 0)}}},
		"dropped":    {{Name: "Bench", Sets: []coach.EaseSet{es(8, 60)}}},
		"no sets":    {{Name: "Bench", Sets: []coach.EaseSet{es(8, 60)}}, {Name: "Pull-up"}},
		"same":       plan,
		"added load": {{Name: "Bench", Sets: []coach.EaseSet{es(8, 60)}}, {Name: "Pull-up", Sets: []coach.EaseSet{es(6, 5)}}},
	} {
		if _, err := coach.CheckEase(plan, got); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	ok := []coach.EaseExercise{{Name: "bench", Sets: []coach.EaseSet{es(8, 52.7)}}, {Name: "Pull-up", Sets: []coach.EaseSet{es(4, 0)}}}
	got, err := coach.CheckEase(plan, ok)
	if err != nil || got[0].Name != "Bench" || got[0].Sets[0].Kg != 52.5 {
		t.Fatalf("easier: %+v %v", got, err)
	}
}

// Mornings like an illness coming on bring "feeling run down?", until it's
// dismissed for the day; with too little history nothing is asked.
func TestRunDown(t *testing.T) {
	a, st, h := testAPI(t)
	ctx := context.Background()
	const date = "2026-11-03"
	day, _ := time.Parse("2006-01-02", date)
	var state json.RawMessage
	// Count only the run-down question: the same request also starts the
	// readiness write-up in the background, which may reach the fake too.
	var asked atomic.Int32
	f := &coachtest.Fake{SystemOne: func(s json.RawMessage, qs map[string]json.RawMessage) map[string]coach.Answer {
		if _, ok := qs["sick"]; ok {
			asked.Add(1)
		}
		state = s
		return map[string]coach.Answer{"sick": {Type: "noul", Noul: 0.95}}
	}}
	a.Coach = &coach.Writer{AI: f.AI(t), KV: st, Log: a.Log}

	put := func(from, n, rhr int, hrv float64) {
		for i := from; i < from+n; i++ {
			d := day.AddDate(0, 0, -i).Format("2006-01-02")
			if err := st.PutRecovery(ctx, store.Recovery{Date: d, Score: 60, HRVms: hrv, RestingHR: rhr, Source: "test"}); err != nil {
				t.Fatal(err)
			}
		}
	}
	put(0, 3, 61, 45) // the last three mornings
	put(3, 10, 52, 70)
	if r := getToday(t, h, date); r.RunDown || asked.Load() != 0 {
		t.Fatalf("10 days of baseline: run down %v, asked %d times", r.RunDown, asked.Load())
	}
	put(13, 18, 52, 70)
	var r todayResp
	for range 400 {
		if r = getToday(t, h, date); r.RunDown {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !r.RunDown {
		t.Fatal("no run-down prompt")
	}
	var sf coach.SickFacts
	_ = json.Unmarshal(state, &sf)
	if sf.BaselineDays != 28 || *sf.RHRBase != 52 || *sf.RHRDelta != 9 || *sf.HRVDeltaPct != -36 || len(sf.Days) != 3 || sf.Days[2].Day != "today" {
		t.Fatalf("facts: %s", state)
	}
	if r.Readiness.Level != "hold" {
		t.Fatalf("readiness changed: %+v", r.Readiness)
	}
	if w := call(t, h, "POST", "/v1/run-down/"+date+"/dismiss", ""); w.Code != 204 {
		t.Fatalf("dismiss: %d", w.Code)
	}
	if r := getToday(t, h, date); r.RunDown {
		t.Fatal("still asked after dismissing")
	}

}
