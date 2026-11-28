package api

import (
	"context"
	"encoding/json"
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

	for _, bad := range []string{"/v1/sleep/trends?weeks=3", "/v1/sleep?from=2026-11-10&to=2026-11-01", "/v1/sleep?from=2024-01-01&to=2026-01-01"} {
		if w := call(t, h, "GET", bad, ""); w.Code != 400 {
			t.Errorf("%s: %d, want 400", bad, w.Code)
		}
	}
}
