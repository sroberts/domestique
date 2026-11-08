package ingest_test

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"domestique/internal/activity"
	"domestique/internal/importer"
	"domestique/internal/ingest"
	"domestique/internal/load"
	"domestique/internal/store"
)

func fixture(t *testing.T, rel string) []byte {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(os.Getenv("HOME"), "go/pkg/mod/github.com/tormoder/fit@*/testdata", rel))
	if len(m) == 0 {
		t.Skip("fixture missing")
	}
	b, err := os.ReadFile(m[0])
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func setup(t *testing.T) (*ingest.Ingester, *store.Store, string) {
	dir := t.TempDir()
	st, err := store.Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return &ingest.Ingester{Store: st, StorageDir: dir, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}, st, dir
}

// gpxFrom renders a parsed ride as a GPX "second recording" with HR only,
// starting offset seconds later, like a watch started a little late.
func gpxFrom(p *activity.Parsed, offset int) []byte {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><gpx creator="Wrist watch"><trk><type>cycling</type><trkseg>`)
	for i, ts := range p.Stream.T {
		if int(ts) < offset || p.Stream.Lat[i] == 0 {
			continue
		}
		hr := p.Stream.HR[i] + 4 // a wrist reads a little high
		at := p.Start.Add(time.Duration(ts) * time.Second).UTC().Format(time.RFC3339)
		fmt.Fprintf(&b, `<trkpt lat="%f" lon="%f"><time>%s</time><extensions><hr>%d</hr></extensions></trkpt>`,
			p.Stream.Lat[i], p.Stream.Lng[i], at, hr)
	}
	b.WriteString(`</trkseg></trk></gpx>`)
	return []byte(b.String())
}

func TestIngestMergeAndStravaExport(t *testing.T) {
	ctx := context.Background()
	in, st, dir := setup(t)
	ride := fixture(t, "dcrainmaker/Edge810-Vector-2013-08-16-15-35-10.fit")

	// 1. The head unit file arrives through a watched folder.
	watchDir := filepath.Join(t.TempDir(), "wahoo")
	fp := filepath.Join(watchDir, "ride.fit")
	old := time.Now().Add(-time.Minute)
	for _, err := range []error{os.MkdirAll(watchDir, 0o755), os.WriteFile(fp, ride, 0o644), os.Chtimes(fp, old, old)} {
		if err != nil {
			t.Fatal(err)
		}
	}
	w := &importer.Watcher{In: in, Dirs: []string{watchDir}}
	if n := w.Scan(ctx); n != 1 {
		t.Fatalf("watch imported %d, want 1", n)
	}
	if n := w.Scan(ctx); n != 0 {
		t.Fatalf("second scan imported %d, want 0", n)
	}

	// 2. A second recording of the same ride merges, and doesn't steal channels.
	p, _ := activity.ParseFIT(ride)
	res, err := in.Ingest(ctx, "watch.gpx", gpxFrom(p, 40), "upload", ingest.Hint{Source: "upload"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Merged {
		t.Fatalf("second recording did not merge: %+v", res)
	}
	m, srcs, err := in.MergedStream(ctx, res.ActivityID)
	if err != nil {
		t.Fatal(err)
	}
	if len(srcs) != 2 || m.Channels["power"] != "garmin" || m.Channels["hr"] != "garmin" {
		t.Fatalf("channels %v from %d sources", m.Channels, len(srcs))
	}

	// 3. A Strava export carrying the same original file (gzipped, as Strava
	//    stores it) is recognised, keeps the athlete's title, and adds a
	//    hand-logged lift with no file.
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	if _, err := zw.Write(ride); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	var zbuf bytes.Buffer
	z := zip.NewWriter(&zbuf)
	csvw, _ := z.Create("activities.csv")
	fmt.Fprint(csvw, "Activity ID,Activity Date,Activity Name,Activity Type,Activity Description,Elapsed Time,Distance,Filename,Distance\n")
	fmt.Fprint(csvw, "111,\"Aug 16, 2013, 6:05:10 PM\",Hilly loop with Ray,Ride,,4700,41.34,activities/111.fit.gz,41339.0\n")
	fmt.Fprint(csvw, "222,\"Aug 18, 2013, 7:00:00 AM\",Gym,Weight Training,,2700,0,,0\n")
	f, _ := z.Create("activities/111.fit.gz")
	if _, err := f.Write(gz.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	zipPath := filepath.Join(t.TempDir(), "export.zip")
	if err := os.WriteFile(zipPath, zbuf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	prog, err := importer.StravaExport(ctx, in, zipPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	if prog.Failed != 0 || prog.Merged != 1 || prog.Imported != 1 {
		t.Fatalf("strava progress %+v", prog)
	}
	acts, _ := st.ListActivities(ctx, store.ActivityQuery{})
	if len(acts) != 2 {
		t.Fatalf("want 2 activities (ride + lift), got %d", len(acts))
	}
	var rideAct store.Activity
	for _, a := range acts {
		if a.Sport == activity.SportRide {
			rideAct = a
		}
	}
	if rideAct.Name != "Hilly loop with Ray" {
		t.Errorf("ride name %q, want the Strava title", rideAct.Name)
	}

	// 4. Load: FTP is detected from the ride and TSS scored by power.
	ath, err := load.Recalc(ctx, st, 190)
	if err != nil {
		t.Fatal(err)
	}
	rideAct, _ = st.GetActivity(ctx, rideAct.ID)
	t.Logf("athlete %+v; ride TSS %.1f IF %.2f via %s", ath, rideAct.TSS, rideAct.Intensity, rideAct.LoadMethod)
	if ath.FTP < 250 || ath.FTP > 320 || rideAct.LoadMethod != "power" || rideAct.TSS < 50 {
		t.Fatalf("load: %+v %+v", ath, rideAct)
	}
	days, err := load.Daily(ctx, st, time.Date(2013, 8, 15, 0, 0, 0, 0, time.UTC), time.Date(2013, 8, 20, 0, 0, 0, 0, time.UTC), time.UTC)
	if err != nil || len(days) != 6 || days[1].TSS == 0 || days[2].CTL == 0 {
		t.Fatalf("daily %v %+v", err, days)
	}

	// Raw files are archived unmodified, content-addressed.
	raws, _ := filepath.Glob(filepath.Join(dir, "raw", "*", "*"))
	if len(raws) != 3 { // fit, gpx, fit.gz
		t.Errorf("raw archive has %d files, want 3: %v", len(raws), raws)
	}
}

// The same Strava activity from a newer export (different bytes, same ID)
// stays one activity; it must not leave the first copy behind as an orphan.
func TestReimportSameSourceKeepsActivity(t *testing.T) {
	in, st, _ := setup(t)
	ctx := context.Background()
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><gpx creator="StravaGPX"><trk><type>1</type><trkseg>`)
	for i := 0; i < 600; i++ {
		fmt.Fprintf(&b, `<trkpt lat="%.5f" lon="-73.0"><time>%s</time></trkpt>`, 45+float64(i)*0.0001,
			time.Date(2024, 5, 1, 10, 0, i, 0, time.UTC).Format(time.RFC3339))
	}
	b.WriteString(`</trkseg></trk></gpx>`)
	h := ingest.Hint{Source: "strava", ExternalID: "42", Name: "Loop"}
	r1, err := in.Ingest(ctx, "42.gpx", []byte(b.String()), "strava-export", h)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := in.Ingest(ctx, "42.gpx", []byte(b.String()+"\n"), "strava-export", h)
	if err != nil {
		t.Fatal(err)
	}
	acts, err := st.ListActivities(ctx, store.ActivityQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if r1.ActivityID != r2.ActivityID || len(acts) != 1 {
		t.Fatalf("re-import split the activity: %s vs %s, %d activities", r1.ActivityID, r2.ActivityID, len(acts))
	}
	// Reported as already there, not as a new activity.
	if r1.Refreshed || !r2.Refreshed || r2.Merged {
		t.Fatalf("first import refreshed=%v; re-import refreshed=%v merged=%v", r1.Refreshed, r2.Refreshed, r2.Merged)
	}
}

// A small gzip that inflates past the file limit is refused, not decoded.
func TestGzipBombRefused(t *testing.T) {
	in, _, dir := setup(t)
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	chunk := make([]byte, 1<<20)
	for i := 0; i < ingest.MaxFileBytes>>20+1; i++ {
		_, _ = zw.Write(chunk)
	}
	zw.Close()
	_, err := in.Ingest(context.Background(), "bomb.fit.gz", buf.Bytes(), "upload", ingest.Hint{Source: "upload"})
	if !errors.Is(err, ingest.ErrTooLarge) {
		t.Fatalf("got %v, want ErrTooLarge", err)
	}
	if m, _ := filepath.Glob(filepath.Join(dir, "raw", "*", "*")); len(m) != 0 {
		t.Fatalf("bomb was stored: %v", m)
	}
}
