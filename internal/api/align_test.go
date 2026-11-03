package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"domestique/internal/store"
	"domestique/internal/strength"
)

func newTestAPI(t *testing.T) (*API, http.Handler) {
	t.Helper()
	st, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	a := &API{Store: st, MaxHR: 190, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Loc: time.UTC}
	mux := http.NewServeMux()
	a.Register(mux)
	return a, mux
}

func do(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	r := httptest.NewRequest(method, path, &buf)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// A phone 4 s fast and a strap 25 s slow: the summary should undo both.
func TestSummaryAlignsHeartRate(t *testing.T) {
	a, h := newTestAPI(t)
	const phoneFast, strapLag = 4_000, 25_000
	t0 := time.Now().Add(-2 * time.Hour).UnixMilli() // device clock
	var sets []store.Set
	tt := t0 + 60_000
	for i := range 8 {
		sets = append(sets, store.Set{ID: fmt.Sprintf("x%d", i), SessionID: "s1", ExerciseName: "Squat", SetIndex: i,
			TargetReps: 5, Reps: 5, WeightKg: 100, StartedAt: tt, EndedAt: tt + 30_000 + int64(i%3)*7_000, UpdatedAt: tt})
		tt = sets[i].EndedAt + 100_000 + int64(i%4)*20_000
	}
	end := tt
	sess := map[string]any{"name": "Test", "template_id": "t", "plan": []store.Exercise{{Name: "Squat", Sets: make([]store.TargetSet, 8)}},
		"started_at": t0, "finished_at": end, "updated_at": end, "clock_offset_ms": -phoneFast}
	if w := do(t, h, "PUT", "/v1/sessions/s1", sess); w.Code != 200 {
		t.Fatalf("put session: %d %s", w.Code, w.Body)
	}
	if do(t, h, "PUT", "/v1/sessions/s1", map[string]any{"name": "x", "plan": sess["plan"], "started_at": t0, "updated_at": end + 1, "clock_offset_ms": int64(7 * 3600 * 1000)}).Code != 400 {
		t.Fatal("an offset of 7 hours should be refused")
	}
	for _, s := range sets {
		if w := do(t, h, "PUT", "/v1/sets/"+s.ID, s); w.Code != 200 {
			t.Fatalf("put set: %d %s", w.Code, w.Body)
		}
	}
	// Heart rate in true time (device time - 4 s), recorded by a strap 25 s slow.
	var hr []store.HRSample
	bpm := 90.0
	for ts := t0 - 120_000; ts < end+120_000; ts += 1000 {
		in := false
		for _, s := range sets {
			in = in || (ts >= s.StartedAt && ts < s.EndedAt)
		}
		if in {
			bpm += (165 - bpm) * 0.07
		} else {
			bpm += (92 - bpm) * 0.02
		}
		if (ts-t0)%5000 == 0 {
			hr = append(hr, store.HRSample{T: ts - phoneFast + strapLag, BPM: int(math.Round(bpm))})
		}
	}
	if err := a.Store.PutHR(context.Background(), "health", hr); err != nil {
		t.Fatal(err)
	}

	var sum strength.Summary
	w := do(t, h, "GET", "/v1/sessions/s1/summary", nil)
	if w.Code != 200 {
		t.Fatalf("summary: %d %s", w.Code, w.Body)
	}
	if v, err := strconv.ParseInt(w.Header().Get("X-Server-Time"), 10, 64); err != nil || math.Abs(float64(v-time.Now().UnixMilli())) > 5000 {
		t.Fatalf("X-Server-Time %q", w.Header().Get("X-Server-Time"))
	}
	_ = json.Unmarshal(w.Body.Bytes(), &sum)
	if sum.Alignment.Source != "auto" || math.Abs(float64(sum.Alignment.HRShiftMs-strapLag)) > 3000 {
		t.Fatalf("alignment %+v, want auto ≈ %d", sum.Alignment, strapLag)
	}
	if sum.Alignment.ClockOffsetMs != -phoneFast || sum.HRStatus != "ready" {
		t.Fatalf("offset %d status %s", sum.Alignment.ClockOffsetMs, sum.HRStatus)
	}

	// A manual shift wins, and null goes back to automatic.
	if w := do(t, h, "PUT", "/v1/sessions/s1/hr-shift", map[string]any{"shift_ms": 20000}); w.Code != 200 {
		t.Fatalf("hr-shift: %d %s", w.Code, w.Body)
	}
	_ = json.Unmarshal(do(t, h, "GET", "/v1/sessions/s1/summary", nil).Body.Bytes(), &sum)
	if sum.Alignment.Source != "manual" || sum.Alignment.HRShiftMs != 20000 {
		t.Fatalf("after manual: %+v", sum.Alignment)
	}
	do(t, h, "PUT", "/v1/sessions/s1/hr-shift", map[string]any{"shift_ms": nil})
	_ = json.Unmarshal(do(t, h, "GET", "/v1/sessions/s1/summary", nil).Body.Bytes(), &sum)
	if sum.Alignment.Source != "auto" {
		t.Fatalf("after reset: %+v", sum.Alignment)
	}
	if do(t, h, "PUT", "/v1/sessions/nope/hr-shift", map[string]any{"shift_ms": 1}).Code != 404 {
		t.Fatal("unknown session should 404")
	}
	if do(t, h, "PUT", "/v1/sessions/s1/hr-shift", map[string]any{"shift_ms": 3_600_000}).Code != 400 {
		t.Fatal("an hour's shift should be refused")
	}
	// A replayed upload can't carry a shift in.
	sess["updated_at"] = end + 5
	sess["hr_shift_ms"] = 99_000
	do(t, h, "PUT", "/v1/sessions/s1", sess)
	if got, _ := a.Store.GetSession(context.Background(), "s1"); got.HRShiftMs != nil {
		t.Fatalf("upload set hr_shift_ms to %d", *got.HRShiftMs)
	}
}
