// Package ingest stores raw workout files, parses them into sources, matches
// sources that record the same workout, and rebuilds the canonical activity.
//
// Raw files are kept unmodified and content-addressed by SHA-256 under
// <storage>/raw. Nothing is ever deleted: a merge only re-points sources.
package ingest

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"domestique/internal/activity"
	"domestique/internal/store"
)

// Ingester serializes imports so matching never races itself.
type Ingester struct {
	Store      *store.Store
	StorageDir string
	Log        *slog.Logger
	mu         sync.Mutex
}

// Hint carries what the origin knows beyond the file itself, e.g. the
// athlete's own title from a Strava export.
type Hint struct {
	Source      string // origin: strava, wahoo, zwift, watch, upload
	ExternalID  string
	Name        string
	Sport       string // free text, mapped with activity.TextSport
	Description string
	Route       string
	Strain      *float64
}

// Result reports what an import did.
type Result struct {
	SourceID   string `json:"source_id"`
	ActivityID string `json:"activity_id"`
	Duplicate  bool   `json:"duplicate"` // the exact file was already imported
	Merged     bool   `json:"merged"`    // joined an existing activity
}

// Ingest imports one file. name is used for its extension (.fit, .gpx.gz, ...).
func (in *Ingester) Ingest(ctx context.Context, name string, data []byte, origin string, h Hint) (Result, error) {
	in.mu.Lock()
	defer in.mu.Unlock()

	sum := sha256.Sum256(data)
	sha := hex.EncodeToString(sum[:])
	if src, err := in.Store.SourceForRaw(ctx, sha); err == nil {
		return Result{SourceID: src.ID, ActivityID: src.ActivityID, Duplicate: true}, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return Result{}, err
	}

	format, plain, err := decode(name, data)
	if err != nil {
		return Result{}, err
	}
	if err := in.saveRaw(ctx, sha, name, data, format, origin); err != nil {
		return Result{}, err
	}

	var p *activity.Parsed
	switch format {
	case "fit":
		p, err = activity.ParseFIT(plain)
	case "gpx":
		p, err = activity.ParseGPX(plain)
	case "tcx":
		p, err = activity.ParseTCX(plain)
	}
	if err != nil {
		return Result{}, fmt.Errorf("%s: %w", filepath.Base(name), err)
	}
	return in.addParsed(ctx, p, &sha, h)
}

// AddManual records an activity that has no file (a Strava entry with no
// upload, a Whoop workout): just a start, a duration and a sport.
func (in *Ingester) AddManual(ctx context.Context, sourceID string, start time.Time, elapsedS int, distanceM float64, h Hint, stream *activity.Stream) (Result, error) {
	in.mu.Lock()
	defer in.mu.Unlock()
	if src, err := in.Store.GetSource(ctx, sourceID); err == nil {
		return Result{SourceID: src.ID, ActivityID: src.ActivityID, Duplicate: true}, nil
	}
	p := &activity.Parsed{Start: start, ElapsedS: elapsedS, MovingS: elapsedS, DistanceM: distanceM, Sport: activity.TextSport(h.Sport, false)}
	if stream != nil {
		p.Stream = *stream
	}
	return in.addParsed(ctx, p, nil, h)
}

func (in *Ingester) addParsed(ctx context.Context, p *activity.Parsed, sha *string, h Hint) (Result, error) {
	src := store.Source{
		Source:    sourceKind(p.Manufacturer, h.Source),
		Sport:     p.Sport,
		Name:      firstNonEmpty(h.Name, p.Name),
		StartAt:   p.Start.UnixMilli(),
		ElapsedS:  p.ElapsedS,
		MovingS:   p.MovingS,
		DistanceM: p.DistanceM,
		Device:    p.Device,
		HasPower:  p.Stream.HasPower(),
		HasHR:     p.Stream.HasHR(),
		HasGPS:    p.Stream.HasGPS(),
		RawSHA256: sha,
	}
	if h.Sport != "" {
		if sp := activity.TextSport(h.Sport, src.HasGPS); sp != activity.SportOther {
			// The athlete's own label wins over the device's guess, except
			// that an indoor file stays indoor.
			if !(sp == activity.SportRide && p.Sport == activity.SportVirtualRide) {
				src.Sport = sp
			}
		}
	}
	extra := map[string]any{}
	if h.Description != "" {
		extra["description"] = h.Description
	}
	if h.Route != "" {
		extra["route"] = h.Route
	}
	if h.Strain != nil {
		extra["strain"] = *h.Strain
	}
	extra["format"] = p.Format
	src.Extra, _ = json.Marshal(extra)
	switch {
	case h.ExternalID != "":
		src.ID = h.Source + ":" + h.ExternalID
		src.ExternalID = h.ExternalID
	case sha != nil:
		src.ID = "file:" + (*sha)[:20]
	default:
		src.ID = "manual:" + randID()
	}

	// Find the activity this records, if one exists.
	actID, merged, err := in.match(ctx, src, &p.Stream)
	if err != nil {
		return Result{}, err
	}
	if actID == "" {
		actID = "a_" + randID()
		// Placeholder so the source's foreign key holds; Rebuild fills it in.
		if err := in.Store.PutActivity(ctx, store.Activity{ID: actID, Sport: src.Sport, Name: src.Name,
			StartAt: src.StartAt, ElapsedS: src.ElapsedS, MovingS: src.MovingS}); err != nil {
			return Result{}, err
		}
	}
	src.ActivityID = actID
	if err := in.Store.PutSource(ctx, src); err != nil {
		return Result{}, err
	}
	if p.Stream.Len() > 0 {
		if err := in.Store.PutStream(ctx, src.ID, p.Stream); err != nil {
			return Result{}, err
		}
	}
	if err := in.Rebuild(ctx, actID); err != nil {
		return Result{}, err
	}
	in.Log.Info("imported", "source", src.ID, "kind", src.Source, "sport", src.Sport,
		"start", p.Start.Format(time.RFC3339), "activity", actID, "merged", merged)
	return Result{SourceID: src.ID, ActivityID: actID, Merged: merged}, nil
}

func (in *Ingester) saveRaw(ctx context.Context, sha, name string, data []byte, format, origin string) error {
	ext := rawExt(name)
	rel := filepath.Join("raw", sha[:2], sha+ext)
	abs := filepath.Join(in.StorageDir, rel)
	if _, err := os.Stat(abs); err != nil {
		if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
			return err
		}
		tmp := abs + ".tmp"
		if err := os.WriteFile(tmp, data, 0o600); err != nil {
			return err
		}
		if err := os.Rename(tmp, abs); err != nil {
			return err
		}
	}
	return in.Store.PutRaw(ctx, sha, rel, int64(len(data)), format, origin)
}

// rawExt keeps the original extension, including .gz, so the archive is the
// file exactly as received.
func rawExt(name string) string {
	l := strings.ToLower(filepath.Base(name))
	for _, e := range []string{".fit.gz", ".gpx.gz", ".tcx.gz", ".fit", ".gpx", ".tcx"} {
		if strings.HasSuffix(l, e) {
			return e
		}
	}
	return filepath.Ext(l)
}

// decode works out the format and gunzips when needed.
func decode(name string, data []byte) (format string, plain []byte, err error) {
	l := strings.ToLower(name)
	plain = data
	if strings.HasSuffix(l, ".gz") || bytes.HasPrefix(data, []byte{0x1f, 0x8b}) {
		zr, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return "", nil, fmt.Errorf("gunzip: %w", err)
		}
		if plain, err = io.ReadAll(io.LimitReader(zr, 256<<20)); err != nil {
			return "", nil, fmt.Errorf("gunzip: %w", err)
		}
		l = strings.TrimSuffix(l, ".gz")
	}
	switch {
	case strings.HasSuffix(l, ".fit") || (len(plain) > 12 && string(plain[8:12]) == ".FIT"):
		return "fit", plain, nil
	case strings.HasSuffix(l, ".gpx"):
		return "gpx", plain, nil
	case strings.HasSuffix(l, ".tcx"):
		return "tcx", plain, nil
	}
	return "", nil, fmt.Errorf("unsupported file type: %s", filepath.Base(name))
}

func sourceKind(mfr, hinted string) string {
	switch mfr {
	case "wahoo", "zwift", "garmin", "whoop":
		return mfr
	}
	if hinted != "" {
		return hinted
	}
	return "device"
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func randID() string {
	b := make([]byte, 9)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ---- Matching ---------------------------------------------------------------------

// MatchWindow and MatchOverlap are the spec's dedup rule: starts within two
// minutes and durations overlapping by more than 80%.
const (
	MatchWindow  = 2 * 60 * 1000
	MatchOverlap = 0.8
)

func compatible(a, b string) bool {
	if a == b {
		return true
	}
	ride := func(s string) bool { return activity.IsRide(s) }
	if ride(a) && ride(b) {
		return true
	}
	// Whoop often labels a ride or lift loosely; "other" matches anything.
	return a == activity.SportOther || b == activity.SportOther
}

func (in *Ingester) match(ctx context.Context, src store.Source, st *activity.Stream) (string, bool, error) {
	cands, err := in.Store.SourcesNear(ctx, src.StartAt, MatchWindow)
	if err != nil {
		return "", false, err
	}
	best, bestScore := "", 0.0
	for _, c := range cands {
		if c.ID == src.ID || !compatible(c.Sport, src.Sport) {
			continue
		}
		ov := overlap(src.StartAt, src.ElapsedS, c.StartAt, c.ElapsedS)
		if ov < MatchOverlap {
			continue
		}
		score := ov
		// Confirm with heart rate when both carry it: two recordings of one
		// workout correlate strongly even when chest and wrist disagree on level.
		if st.HasHR() && c.HasHR {
			var other activity.Stream
			if err := in.Store.GetStream(ctx, c.ID, &other); err == nil {
				r, n := hrCorrelation(src.StartAt, st, c.StartAt, &other)
				if n >= 120 && r < 0.3 {
					continue // two different workouts that happen to coincide
				}
				if n >= 120 {
					score += r
				}
			}
		}
		if score > bestScore {
			best, bestScore = c.ActivityID, score
		}
	}
	return best, best != "", nil
}

func overlap(aStart int64, aDur int, bStart int64, bDur int) float64 {
	aEnd := aStart + int64(aDur)*1000
	bEnd := bStart + int64(bDur)*1000
	lo, hi := max(aStart, bStart), min(aEnd, bEnd)
	shorter := min(aEnd-aStart, bEnd-bStart)
	if hi <= lo || shorter <= 0 {
		return 0
	}
	return float64(hi-lo) / float64(shorter)
}

// hrCorrelation aligns two HR streams on wall-clock time and returns Pearson r.
func hrCorrelation(aStart int64, a *activity.Stream, bStart int64, b *activity.Stream) (float64, int) {
	bAt := map[int64]int32{}
	for i, t := range b.T {
		if b.HR[i] > 0 {
			bAt[bStart/1000+int64(t)] = b.HR[i]
		}
	}
	var xs, ys []float64
	for i, t := range a.T {
		if a.HR[i] <= 0 {
			continue
		}
		if y, ok := bAt[aStart/1000+int64(t)]; ok {
			xs = append(xs, float64(a.HR[i]))
			ys = append(ys, float64(y))
		}
	}
	n := len(xs)
	if n < 2 {
		return 0, n
	}
	var mx, my float64
	for i := range xs {
		mx += xs[i]
		my += ys[i]
	}
	mx /= float64(n)
	my /= float64(n)
	var sxy, sxx, syy float64
	for i := range xs {
		dx, dy := xs[i]-mx, ys[i]-my
		sxy += dx * dy
		sxx += dx * dx
		syy += dy * dy
	}
	if sxx == 0 || syy == 0 {
		return 0, n
	}
	return sxy / math.Sqrt(sxx*syy), n
}

// AddWhoopWorkout records or refreshes a Whoop workout. Whoop revises scores
// after the fact, so an existing source is updated rather than skipped. The
// API has no HR stream, so the summary (avg, max, zone time) rides in Extra.
func (in *Ingester) AddWhoopWorkout(ctx context.Context, id string, start time.Time, elapsedS int, distanceM float64,
	h Hint, avgHR, maxHR int, zonesMs [6]int64) (Result, error) {
	extra := map[string]any{"avg_hr": avgHR, "max_hr": maxHR, "zones_ms": zonesMs}
	if h.Strain != nil {
		extra["strain"] = *h.Strain
	}
	srcID := "whoop:" + id
	in.mu.Lock()
	if prev, err := in.Store.GetSource(ctx, srcID); err == nil {
		prev.Extra, _ = json.Marshal(extra)
		prev.ElapsedS = elapsedS
		err := in.Store.PutSource(ctx, prev)
		if err == nil {
			err = in.Rebuild(ctx, prev.ActivityID)
		}
		in.mu.Unlock()
		return Result{SourceID: srcID, ActivityID: prev.ActivityID, Duplicate: true}, err
	}
	in.mu.Unlock()

	p := &activity.Parsed{Start: start, ElapsedS: elapsedS, MovingS: elapsedS, DistanceM: distanceM,
		Sport: activity.TextSport(h.Sport, false)}
	in.mu.Lock()
	defer in.mu.Unlock()
	res, err := in.addParsed(ctx, p, nil, h)
	if err != nil {
		return res, err
	}
	src, err := in.Store.GetSource(ctx, res.SourceID)
	if err != nil {
		return res, err
	}
	src.Extra, _ = json.Marshal(extra)
	if err := in.Store.PutSource(ctx, src); err != nil {
		return res, err
	}
	return res, in.Rebuild(ctx, res.ActivityID)
}
