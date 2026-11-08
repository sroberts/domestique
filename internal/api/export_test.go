package api

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tormoder/fit"

	"domestique/internal/importer"
	"domestique/internal/ingest"
	"domestique/internal/store"
)

type harness struct {
	t   *testing.T
	a   *API
	mux *http.ServeMux
	st  *store.Store
	in  *ingest.Ingester
	gz  []byte // the original ride as archived (gzipped, like Strava keeps it)
}

func fixtureFIT(t *testing.T) []byte {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(os.Getenv("HOME"), "go/pkg/mod/github.com/tormoder/fit@*/testdata/dcrainmaker/Edge810-Vector-2013-08-16-15-35-10.fit"))
	if len(m) == 0 {
		t.Skip("fixture missing")
	}
	b, err := os.ReadFile(m[0])
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// stravaZip is a Strava bulk export: the fixture ride (gzipped) and a ride
// logged by hand with no file, each with gear when withGear is set.
func stravaZip(t *testing.T, gz []byte, withGear bool) string {
	var zbuf bytes.Buffer
	z := zip.NewWriter(&zbuf)
	csvw, _ := z.Create("activities.csv")
	fmt.Fprint(csvw, "Activity ID,Activity Date,Activity Name,Activity Type,Activity Description,Elapsed Time,Distance,Filename,Activity Gear,Distance\n")
	gear := func(g string) string {
		if withGear {
			return g
		}
		return ""
	}
	fmt.Fprintf(csvw, "111,\"Aug 16, 2013, 6:05:10 PM\",Hilly loop with Ray,Ride,,4700,41.34,activities/111.fit.gz,%s,41339.0\n", gear("Specialized  Tarmac"))
	fmt.Fprintf(csvw, "333,\"Aug 22, 2013, 7:00:00 AM\",Café ride,Ride,,3600,30.0,,%s,30000.0\n", gear("Hire bike"))
	f, _ := z.Create("activities/111.fit.gz")
	if _, err := f.Write(gz); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "export.zip")
	if err := os.WriteFile(p, zbuf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func newHarness(t *testing.T) *harness {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	in := &ingest.Ingester{Store: st, StorageDir: dir, Log: log, Loc: time.UTC}
	a := &API{Store: st, Ingest: in, Log: log, Loc: time.UTC, MaxHR: 190}
	mux := http.NewServeMux()
	a.Register(mux)
	h := &harness{t: t, a: a, mux: mux, st: st, in: in}

	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	if _, err := zw.Write(fixtureFIT(t)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	h.gz = gz.Bytes()

	// First an export from before gear was kept, then a newer one with gear:
	// the second must teach the existing records their bikes.
	for _, withGear := range []bool{false, true} {
		if _, err := importer.StravaExport(ctx, in, stravaZip(t, h.gz, withGear), nil); err != nil {
			t.Fatal(err)
		}
	}

	// A strength session logged in the app, with heart rate.
	start := time.Date(2013, 8, 20, 18, 0, 0, 0, time.UTC).UnixMilli()
	fin := start + 30*60*1000
	if err := st.PutSession(ctx, store.Session{ID: "sess1", TemplateID: "t", Name: "Lower A",
		Plan:      []store.Exercise{{Name: "Squat", Sets: []store.TargetSet{{Reps: 5, WeightKg: 100}}}},
		StartedAt: start, FinishedAt: &fin, UpdatedAt: fin}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		s := start + int64(i)*8*60*1000
		if err := st.PutSet(ctx, store.Set{ID: fmt.Sprintf("set%d", i), SessionID: "sess1", ExerciseName: "Squat",
			SetIndex: i, TargetReps: 5, TargetWeightKg: 100, Reps: 5, WeightKg: 100, StartedAt: s, EndedAt: s + 40_000, UpdatedAt: fin}); err != nil {
			t.Fatal(err)
		}
	}
	var hr []store.HRSample
	for ts := start; ts < fin; ts += 1000 {
		hr = append(hr, store.HRSample{T: ts, BPM: 100 + int((ts-start)/1000%50)})
	}
	if err := st.PutHR(ctx, "health", hr); err != nil {
		t.Fatal(err)
	}
	if err := in.UpsertSession(ctx, "sess1"); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *harness) do(method, path string, body any) *httptest.ResponseRecorder {
	h.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	w := httptest.NewRecorder()
	h.mux.ServeHTTP(w, httptest.NewRequest(method, path, rd))
	return w
}

func (h *harness) rideID() string {
	src, err := h.st.GetSource(context.Background(), "strava:111")
	if err != nil {
		h.t.Fatal(err)
	}
	return src.ActivityID
}

func readZip(t *testing.T, b []byte) map[string][]byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatalf("not a zip: %v", err)
	}
	out := map[string][]byte{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		out[f.Name], _ = io.ReadAll(rc)
		rc.Close()
	}
	return out
}

func TestRangeExportZip(t *testing.T) {
	h := newHarness(t)
	// A bike so activities.csv can name it.
	if w := h.do("PUT", "/v1/gear/bikes/tarmac", map[string]any{"name": "Tarmac", "aliases": []string{"Specialized Tarmac"}}); w.Code != 200 {
		t.Fatalf("put bike: %d %s", w.Code, w.Body)
	}

	w := h.do("GET", "/v1/export?from=2013-08-01&to=2013-08-31", nil)
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/zip" ||
		w.Header().Get("Content-Disposition") != `attachment; filename="domestique-2013-08-01-to-2013-08-31.zip"` {
		t.Fatalf("export: %d %v", w.Code, w.Header())
	}
	files := readZip(t, w.Body.Bytes())
	rows, err := csv.NewReader(bytes.NewReader(files["activities.csv"])).ReadAll()
	if err != nil || len(rows) != 4 { // header + ride + manual ride + lift
		t.Fatalf("activities.csv: %v %d rows: %v", err, len(rows), rows)
	}
	col := map[string]int{}
	for i, c := range rows[0] {
		col[c] = i
	}
	ride := rows[1]
	if ride[col["name"]] != "Hilly loop with Ray" || ride[col["bike"]] != "Tarmac" || ride[col["sources"]] != "garmin" ||
		ride[col["normalized_power_w"]] == "" {
		t.Fatalf("ride row: %v", ride)
	}
	fitName := ride[col["files"]]
	if fitName != "activities/2013-08-16-hilly-loop-with-ray.fit" {
		t.Fatalf("ride file %q", fitName)
	}
	f, err := fit.Decode(bytes.NewReader(files[fitName]))
	if err != nil {
		t.Fatalf("generated FIT doesn't decode: %v", err)
	}
	act, _ := f.Activity()
	m, _, _ := h.in.MergedStream(context.Background(), h.rideID())
	if len(act.Records) != m.Stream.Len() || len(act.Sessions) != 1 || len(act.Laps) < 1 {
		t.Fatalf("FIT has %d records (want %d), %d sessions, %d laps", len(act.Records), m.Stream.Len(), len(act.Sessions), len(act.Laps))
	}
	// The manual ride has no samples: a row, no file. The lift has heart rate: a FIT.
	if rows[3][col["files"]] != "" || rows[3][col["bike"]] != "" || rows[3][col["name"]] != "Café ride" {
		t.Fatalf("manual ride row: %v", rows[3])
	}
	if rows[2][col["sport"]] != "strength" || !strings.HasSuffix(rows[2][col["files"]], ".fit") {
		t.Fatalf("lift row: %v", rows[2])
	}
	sets, _ := csv.NewReader(bytes.NewReader(files["sets.csv"])).ReadAll()
	if len(sets) != 4 || sets[1][3] != "Squat" || sets[1][9] != "2013-08-20T18:00:00Z" {
		t.Fatalf("sets.csv: %v", sets)
	}

	// Originals: the archived file, byte for byte.
	w = h.do("GET", "/v1/export?from=2013-08-01&to=2013-08-31&files=originals", nil)
	files = readZip(t, w.Body.Bytes())
	orig := files["activities/2013-08-16-hilly-loop-with-ray-garmin.fit.gz"]
	if !bytes.Equal(orig, h.gz) {
		t.Fatalf("original in zip differs (%d bytes, want %d); files %v", len(orig), len(h.gz), keys(files))
	}

	// An empty range is still a valid archive.
	w = h.do("GET", "/v1/export?from=2020-01-01&to=2020-01-31", nil)
	if files := readZip(t, w.Body.Bytes()); len(files) != 2 {
		t.Fatalf("empty range: %v", keys(files))
	}

	for _, q := range []string{"from=2013-08-31&to=2013-08-01", "from=2010-01-01&to=2016-01-02", "from=2013-8-1&to=2013-08-31",
		"from=2013-08-01&to=2013-08-31&files=all"} {
		if w := h.do("GET", "/v1/export?"+q, nil); w.Code != 400 {
			t.Errorf("%s: %d, want 400", q, w.Code)
		}
	}
}

func keys(m map[string][]byte) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestActivityExports(t *testing.T) {
	h := newHarness(t)
	id := h.rideID()

	w := h.do("GET", "/v1/activities/"+id+"/export?format=fit", nil)
	if w.Code != 200 || w.Header().Get("Content-Disposition") != `attachment; filename="2013-08-16-hilly-loop-with-ray.fit"` {
		t.Fatalf("fit: %d %v", w.Code, w.Header())
	}
	if _, err := fit.Decode(bytes.NewReader(w.Body.Bytes())); err != nil {
		t.Fatal(err)
	}
	w = h.do("GET", "/v1/activities/"+id+"/export?format=csv", nil)
	rows, _ := csv.NewReader(w.Body).ReadAll()
	if w.Code != 200 || len(rows) < 100 || rows[0][0] != "time" || !strings.Contains(strings.Join(rows[0], ","), "power_w") {
		t.Fatalf("csv: %d, %d rows, %v", w.Code, len(rows), rows[0])
	}
	if w := h.do("GET", "/v1/activities/"+id+"/export?format=sets", nil); w.Code != 404 {
		t.Fatalf("sets of a ride: %d", w.Code)
	}
	if w := h.do("GET", "/v1/activities/"+id+"/export?format=gpx", nil); w.Code != 400 {
		t.Fatalf("unknown format: %d", w.Code)
	}

	// The original, unmodified.
	w = h.do("GET", "/v1/sources/strava:111/raw", nil)
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), h.gz) ||
		w.Header().Get("Content-Disposition") != `attachment; filename="2013-08-16-hilly-loop-with-ray-garmin.fit.gz"` {
		t.Fatalf("raw: %d %v", w.Code, w.Header())
	}
	if w := h.do("GET", "/v1/sources/strava:333/raw", nil); w.Code != 404 {
		t.Fatalf("raw of a manual entry: %d", w.Code)
	}
	if w := h.do("GET", "/v1/sources/..%2F..%2Fetc/raw", nil); w.Code != 400 && w.Code != 404 {
		t.Fatalf("traversal id: %d", w.Code)
	}

	// A tampered archive path can't escape the raw directory.
	raw, _ := h.st.GetRaw(context.Background(), *mustSource(t, h.st, "strava:111").RawSHA256)
	raw.Path = "raw/../db/domestique.sqlite"
	if _, err := h.a.openRaw(raw); err == nil {
		t.Fatal("openRaw followed ..")
	}

	// Strength: sets CSV and a heart-rate FIT with a lap per set.
	w = h.do("GET", "/v1/sessions/sess1/export?format=csv", nil)
	sets, _ := csv.NewReader(w.Body).ReadAll()
	if w.Code != 200 || len(sets) != 4 || w.Header().Get("Content-Disposition") != `attachment; filename="2013-08-20-lower-a-sets.csv"` {
		t.Fatalf("sets csv: %d %v %v", w.Code, w.Header(), sets)
	}
	w = h.do("GET", "/v1/sessions/sess1/export?format=fit", nil)
	if w.Code != 200 {
		t.Fatalf("session fit: %d %s", w.Code, w.Body)
	}
	f, err := fit.Decode(bytes.NewReader(w.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	act, _ := f.Activity()
	if len(act.Laps) != 3 || act.Sessions[0].Sport != fit.SportTraining || act.Records[0].HeartRate != 100 {
		t.Fatalf("session fit: %d laps, sport %v, hr %d", len(act.Laps), act.Sessions[0].Sport, act.Records[0].HeartRate)
	}
}

func mustSource(t *testing.T, st *store.Store, id string) store.Source {
	s, err := st.GetSource(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
