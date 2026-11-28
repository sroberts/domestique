package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"domestique/internal/store"
)

func TestSleepEndpoints(t *testing.T) {
	a, st, h := testAPI(t)
	ctx := context.Background()
	now := time.Now().In(a.loc())
	for i := range 3 {
		d := now.AddDate(0, 0, -i)
		end := time.Date(d.Year(), d.Month(), d.Day(), 7, 0, 0, 0, a.loc())
		rr := 15.0
		if err := st.PutSleep(ctx, store.Sleep{ID: "whoop:n" + d.Format("0102"), Date: d.Format("2006-01-02"),
			StartAt: end.Add(-8 * time.Hour).UnixMilli(), EndAt: end.UnixMilli(), AsleepS: 7 * 3600, Source: "whoop",
			RespiratoryRate: &rr, DeepS: 5400, REMS: 6000, Need: &store.SleepNeed{BaselineS: 27000, DebtS: 900}}); err != nil {
			t.Fatal(err)
		}
	}
	nap := now.Add(-2 * time.Hour)
	_ = st.PutSleep(ctx, store.Sleep{ID: "whoop:nap", Date: now.Format("2006-01-02"), StartAt: nap.UnixMilli(),
		EndAt: nap.Add(30 * time.Minute).UnixMilli(), AsleepS: 1500, Source: "whoop", Nap: true})

	w := call(t, h, "GET", "/v1/sleep", "")
	var list struct {
		Nights []store.Sleep `json:"nights"`
		Naps   []store.Sleep `json:"naps"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &list) != nil || len(list.Nights) != 3 || len(list.Naps) != 1 {
		t.Fatalf("sleep %d %s", w.Code, w.Body.String())
	}
	if list.Nights[0].Need == nil || list.Nights[0].Need.TotalS() != 27900 {
		t.Fatalf("need not carried: %+v", list.Nights[0])
	}

	w = call(t, h, "GET", "/v1/sleep/trends?weeks=4", "")
	var tr struct {
		Weeks    int              `json:"weeks"`
		Last     *store.Sleep     `json:"last"`
		LastNaps []store.Sleep    `json:"last_naps"`
		Nights   []map[string]any `json:"nights"`
		Debt     struct{ S int }  `json:"debt"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &tr) != nil {
		t.Fatalf("trends %d %s", w.Code, w.Body.String())
	}
	if tr.Weeks != 4 || tr.Last == nil || len(tr.LastNaps) != 1 || len(tr.Nights) != 3 || tr.Debt.S != 3*1800 {
		t.Fatalf("trends %s", w.Body.String())
	}

	// Only to: from is 13 days before it, not before today.
	if w := call(t, h, "GET", "/v1/sleep?to=2025-03-01", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"from":"2025-02-16"`) {
		t.Fatalf("only to: %d %s", w.Code, w.Body.String())
	}

	for _, bad := range []string{"/v1/sleep/trends?weeks=3", "/v1/sleep?from=2026-11-10&to=2026-11-01", "/v1/sleep?from=2024-01-01&to=2026-01-01"} {
		if w := call(t, h, "GET", bad, ""); w.Code != 400 {
			t.Errorf("%s: %d, want 400", bad, w.Code)
		}
	}
}

func TestEveningLog(t *testing.T) {
	a, _, h := testAPI(t)
	today := time.Now().In(a.loc()).Format("2006-01-02")
	w := call(t, h, "GET", "/v1/evenings/"+today, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"logged":false`) || !strings.Contains(w.Body.String(), `"late_meal"`) {
		t.Fatalf("unlogged evening: %d %s", w.Code, w.Body.String())
	}
	w = call(t, h, "PUT", "/v1/evenings/"+today, `{"items":["alcohol","screens","alcohol"]}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"items":["alcohol","screens"]`) {
		t.Fatalf("log: %d %s", w.Code, w.Body.String())
	}
	// A clear evening is logged, with no items.
	w = call(t, h, "PUT", "/v1/evenings/"+today, `{"items":[]}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"logged":true,"items":[]`) {
		t.Fatalf("clear: %d %s", w.Code, w.Body.String())
	}
	if w = call(t, h, "DELETE", "/v1/evenings/"+today, ""); w.Code != 204 {
		t.Fatalf("delete: %d", w.Code)
	}
	if w = call(t, h, "GET", "/v1/evenings/"+today, ""); !strings.Contains(w.Body.String(), `"logged":false`) {
		t.Fatalf("after delete: %s", w.Body.String())
	}
	tomorrow := time.Now().In(a.loc()).AddDate(0, 0, 1).Format("2006-01-02")
	for _, c := range []struct{ path, body string }{
		{"/v1/evenings/" + today, `{"items":["coffee"]}`},
		{"/v1/evenings/" + tomorrow, `{"items":[]}`},
		{"/v1/evenings/yesterday", `{"items":[]}`},
	} {
		if w := call(t, h, "PUT", c.path, c.body); w.Code != 400 {
			t.Errorf("%s %s: %d, want 400", c.path, c.body, w.Code)
		}
	}
}

func TestBedtimeAndWakeTimes(t *testing.T) {
	a, st, h := testAPI(t)
	ctx := context.Background()
	now := time.Now().In(a.loc())
	eve := now.Format("2006-01-02")
	if w := call(t, h, "GET", "/v1/bedtime?date="+eve, ""); w.Code != 204 {
		t.Fatalf("no nights: %d, want 204", w.Code)
	}
	eff := 90
	for i := range 10 {
		d := now.AddDate(0, 0, -i)
		end := time.Date(d.Year(), d.Month(), d.Day(), 6, 30, 0, 0, a.loc())
		_ = st.PutSleep(ctx, store.Sleep{ID: "whoop:b" + d.Format("0102"), Date: d.Format("2006-01-02"),
			StartAt: end.Add(-500 * time.Minute).UnixMilli(), EndAt: end.UnixMilli(), AsleepS: 27000, Source: "whoop",
			Efficiency: &eff, Need: &store.SleepNeed{BaselineS: 27000}})
	}
	w := call(t, h, "GET", "/v1/bedtime?date="+eve, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"bed_by":"22:10"`) || !strings.Contains(w.Body.String(), `"wake_from":"usual"`) {
		t.Fatalf("bedtime %d %s", w.Code, w.Body.String())
	}
	if v, err := st.SyncState(ctx, "bedtime:"+eve); err != nil || v != "27000" {
		t.Fatalf("estimate kept %q %v", v, err)
	}
	// Any other evening is advice only: nothing kept.
	old := now.AddDate(0, 0, -5).Format("2006-01-02")
	if w := call(t, h, "GET", "/v1/bedtime?date="+old, ""); w.Code != 200 {
		t.Fatalf("past bedtime %d", w.Code)
	}
	if v, _ := st.SyncState(ctx, "bedtime:"+old); v != "" {
		t.Fatalf("past evening's estimate written: %q", v)
	}

	day := []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}[now.AddDate(0, 0, 1).Weekday()]
	if w := call(t, h, "PUT", "/v1/wake-times", `{"wake_times":{"`+day+`":"05:45"}}`); w.Code != 200 {
		t.Fatalf("set wake: %d %s", w.Code, w.Body.String())
	}
	w = call(t, h, "GET", "/v1/bedtime?date="+eve, "")
	if !strings.Contains(w.Body.String(), `"bed_by":"21:25"`) || !strings.Contains(w.Body.String(), `"wake_from":"set"`) {
		t.Fatalf("with wake set: %s", w.Body.String())
	}
	for _, bad := range []string{`{"wake_times":{"monday":"06:00"}}`, `{"wake_times":{"mon":"6:00"}}`, `{"wake_times":{"mon":"15:00"}}`} {
		if w := call(t, h, "PUT", "/v1/wake-times", bad); w.Code != 400 {
			t.Errorf("%s: %d, want 400", bad, w.Code)
		}
	}
	if w := call(t, h, "PUT", "/v1/wake-times", `{"wake_times":{}}`); w.Code != 200 || !strings.Contains(w.Body.String(), `"wake_times":{}`) {
		t.Fatalf("clear: %d %s", w.Code, w.Body.String())
	}
}
