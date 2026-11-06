package api

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tormoder/fit"

	"domestique/internal/activity"
	"domestique/internal/export"
	"domestique/internal/gear"
	"domestique/internal/ingest"
	"domestique/internal/store"
)

// Export: standard FIT and CSV of any activity or date range, and every
// original file exactly as received, so the data never locks in.
func (a *API) registerExport(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/activities/{id}/export", a.exportActivity)
	mux.HandleFunc("GET /v1/sessions/{id}/export", a.exportSession)
	mux.HandleFunc("GET /v1/sources/{id}/raw", a.rawSource)
	mux.HandleFunc("GET /v1/export", a.exportRange)
}

// MaxExportDays bounds a range export: five years.
const MaxExportDays = 5*366 + 1

var (
	errNoSamples = errors.New("no recorded samples")
	sourceIDRe   = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)
)

// attachment sets the headers for a download. name must already be safe
// (export.FileName produces lower-case ASCII, digits and dashes).
func attachment(w http.ResponseWriter, name, contentType string) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

func (a *API) fileName(startMs int64, name, sport, ext string) string {
	t := time.UnixMilli(startMs).In(a.loc())
	return export.FileName(t, name, ingest.DefaultName(sport, t), ext)
}

// activityFIT renders an activity's canonical merged stream as a FIT file.
func (a *API) activityFIT(ctx context.Context, act store.Activity) ([]byte, error) {
	m, srcs, err := a.Ingest.MergedStream(ctx, act.ID)
	if err != nil {
		return nil, err
	}
	if m.Stream.Len() == 0 {
		return nil, errNoSamples
	}
	var met activity.Metrics
	_ = json.Unmarshal(act.Metrics, &met)
	fa := export.FITActivity{
		Start: time.UnixMilli(m.StartAt), Sport: act.Sport, Stream: &m.Stream,
		ElapsedS: act.ElapsedS, MovingS: act.MovingS, DistanceM: act.DistanceM,
		AscentM: met.AscentM, KJ: met.KJ, NP: met.NP, Loc: a.loc(),
	}
	if act.LoadMethod == "power" {
		fa.TSS, fa.IF, fa.FTP = act.TSS, act.Intensity, act.FTP
	}
	if act.SessionID != nil {
		fa.Laps = a.setLaps(ctx, *act.SessionID, m.StartAt)
	} else {
		fa.Laps = a.originalLaps(ctx, srcs, m.StartAt)
	}
	var buf bytes.Buffer
	if err := export.WriteFIT(&buf, fa); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// originalLaps reads the laps of the original FIT file the stream is based
// on, so laps pressed on the head unit survive the round trip.
func (a *API) originalLaps(ctx context.Context, srcs []store.Source, startAt int64) []export.Lap {
	for _, s := range srcs {
		if s.RawSHA256 == nil || s.StartAt != startAt {
			continue
		}
		raw, err := a.Store.GetRaw(ctx, *s.RawSHA256)
		if err != nil || raw.Format != "fit" {
			continue
		}
		f, err := a.openRaw(raw)
		if err != nil {
			continue
		}
		var r io.Reader = f
		if strings.HasSuffix(raw.Path, ".gz") {
			zr, err := gzip.NewReader(f)
			if err != nil {
				f.Close()
				continue
			}
			r = zr
		}
		data, err := ingest.ReadLimited(r)
		f.Close()
		if err != nil {
			continue
		}
		file, err := fit.Decode(bytes.NewReader(data))
		if err != nil {
			continue
		}
		act, err := file.Activity()
		if err != nil || len(act.Laps) < 2 {
			return nil
		}
		laps := make([]export.Lap, 0, len(act.Laps))
		for _, l := range act.Laps {
			off := int32(l.StartTime.UnixMilli()-startAt) / 1000
			laps = append(laps, export.Lap{Start: off, End: off + int32(l.TotalElapsedTime/1000)})
		}
		return laps
	}
	return nil
}

// setLaps makes each logged set (with the rest after it) a lap.
func (a *API) setLaps(ctx context.Context, sessionID string, startAt int64) []export.Lap {
	sess, err := a.Store.GetSession(ctx, sessionID)
	// Ingest puts the app source on true time (phone time + clock offset).
	if err != nil || sess.StartedAt+sess.ClockOffsetMs != startAt {
		return nil // the stream isn't on the session's clock: one lap is honest
	}
	sets, err := a.Store.SessionSets(ctx, sessionID)
	if err != nil || len(sets) < 2 {
		return nil
	}
	laps := []export.Lap{{Start: 0}}
	for _, s := range sets {
		off := int32((s.StartedAt + sess.ClockOffsetMs - startAt) / 1000)
		if off > laps[len(laps)-1].Start {
			laps = append(laps, export.Lap{Start: off})
		}
	}
	return laps
}

// openRaw opens an archived original, refusing anything that isn't a plain
// file inside <storage>/raw.
func (a *API) openRaw(raw store.RawFile) (*os.File, error) {
	rel := filepath.Clean(raw.Path)
	if !filepath.IsLocal(rel) || !strings.HasPrefix(rel, "raw"+string(filepath.Separator)) {
		return nil, fmt.Errorf("raw file %s is outside the archive", raw.SHA256)
	}
	root, err := os.OpenRoot(filepath.Join(a.Ingest.StorageDir, "raw"))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	inner := strings.TrimPrefix(rel, "raw"+string(filepath.Separator))
	fi, err := root.Lstat(inner)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("raw file %s is not a regular file", raw.SHA256)
	}
	return root.Open(inner) // os.Root refuses paths and symlinks that leave raw/
}

func rawExt(path string) string {
	l := strings.ToLower(filepath.Base(path))
	for _, e := range []string{".fit.gz", ".gpx.gz", ".tcx.gz", ".fit", ".gpx", ".tcx", ".json"} {
		if strings.HasSuffix(l, e) {
			return e[1:]
		}
	}
	return "bin"
}

func rawType(ext string) string {
	switch {
	case strings.HasSuffix(ext, ".gz"):
		return "application/gzip"
	case ext == "gpx" || ext == "tcx":
		return "application/xml"
	case ext == "json":
		return "application/json"
	}
	return "application/octet-stream"
}

// ---- One activity ----------------------------------------------------------------

func (a *API) exportActivity(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	act, err := a.Store.GetActivity(ctx, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "no such activity")
		return
	} else if err != nil {
		a.fail(w, r, err)
		return
	}
	switch format := r.URL.Query().Get("format"); format {
	case "", "fit":
		data, err := a.activityFIT(ctx, act)
		if errors.Is(err, errNoSamples) {
			writeErr(w, http.StatusNotFound, "this activity has no recorded samples to export")
			return
		} else if err != nil {
			a.fail(w, r, err)
			return
		}
		attachment(w, a.fileName(act.StartAt, act.Name, act.Sport, "fit"), "application/vnd.ant.fit")
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		_, _ = w.Write(data)
	case "csv":
		m, _, err := a.Ingest.MergedStream(ctx, act.ID)
		if err != nil {
			a.fail(w, r, err)
			return
		}
		if m.Stream.Len() == 0 {
			writeErr(w, http.StatusNotFound, "this activity has no recorded samples to export")
			return
		}
		attachment(w, a.fileName(act.StartAt, act.Name, act.Sport, "csv"), "text/csv; charset=utf-8")
		if err := export.StreamCSV(w, time.UnixMilli(m.StartAt), &m.Stream); err != nil {
			a.Log.Warn("export csv", "activity", act.ID, "err", err.Error())
		}
	case "sets":
		if act.SessionID == nil {
			writeErr(w, http.StatusNotFound, "sets exist only for strength sessions logged in the app")
			return
		}
		a.writeSetsCSV(w, r, *act.SessionID)
	default:
		writeErr(w, http.StatusBadRequest, "format must be fit, csv or sets")
	}
}

func (a *API) exportSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !idRe.MatchString(id) {
		writeErr(w, http.StatusBadRequest, "bad session id")
		return
	}
	switch r.URL.Query().Get("format") {
	case "", "csv":
		a.writeSetsCSV(w, r, id)
	case "fit":
		act, err := a.Store.ActivityBySession(r.Context(), id)
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "this session hasn't reached the activity store yet")
			return
		} else if err != nil {
			a.fail(w, r, err)
			return
		}
		data, err := a.activityFIT(r.Context(), act)
		if errors.Is(err, errNoSamples) {
			writeErr(w, http.StatusNotFound, "no heart rate for this session yet, so there is nothing to put in a FIT file")
			return
		} else if err != nil {
			a.fail(w, r, err)
			return
		}
		attachment(w, a.fileName(act.StartAt, act.Name, act.Sport, "fit"), "application/vnd.ant.fit")
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		_, _ = w.Write(data)
	default:
		writeErr(w, http.StatusBadRequest, "format must be csv or fit")
	}
}

func (a *API) writeSetsCSV(w http.ResponseWriter, r *http.Request, sessionID string) {
	ctx := r.Context()
	sess, err := a.Store.GetSession(ctx, sessionID)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "no such session")
		return
	} else if err != nil {
		a.fail(w, r, err)
		return
	}
	sets, err := a.Store.SessionSets(ctx, sessionID)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	name := a.fileName(sess.StartedAt+sess.ClockOffsetMs, sess.Name, activity.SportStrength, "csv")
	attachment(w, strings.TrimSuffix(name, ".csv")+"-sets.csv", "text/csv; charset=utf-8")
	c, err := export.NewSetsCSV(w, a.loc())
	if err == nil {
		err = c.Session(sess, sets)
	}
	if err == nil {
		err = c.Close()
	}
	if err != nil {
		a.Log.Warn("export sets", "session", sessionID, "err", err.Error())
	}
}

// rawSource serves one original file, byte for byte.
func (a *API) rawSource(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	if !sourceIDRe.MatchString(id) {
		writeErr(w, http.StatusBadRequest, "bad source id")
		return
	}
	src, err := a.Store.GetSource(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "no such source")
		return
	} else if err != nil {
		a.fail(w, r, err)
		return
	}
	if src.RawSHA256 == nil {
		writeErr(w, http.StatusNotFound, "this record came without a file (an API summary or a manual entry)")
		return
	}
	raw, err := a.Store.GetRaw(ctx, *src.RawSHA256)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "the original file isn't in the archive")
		return
	} else if err != nil {
		a.fail(w, r, err)
		return
	}
	f, err := a.openRaw(raw)
	if errors.Is(err, os.ErrNotExist) {
		writeErr(w, http.StatusNotFound, "the original file is missing from the storage directory")
		return
	} else if err != nil {
		a.fail(w, r, err)
		return
	}
	defer f.Close()
	ext := rawExt(raw.Path)
	name := strings.TrimSuffix(a.fileName(src.StartAt, src.Name, src.Sport, "x"), ".x") + "-" + export.Slug(src.Source) + "." + ext
	attachment(w, name, rawType(ext))
	if fi, err := f.Stat(); err == nil {
		w.Header().Set("Content-Length", strconv.FormatInt(fi.Size(), 10))
	}
	_, _ = io.Copy(w, f)
}

// ---- A date range, as a zip ---------------------------------------------------------

// exportRange streams a zip of every activity in [from, to] (athlete's local
// dates): activities.csv, sets.csv and one file per activity under
// activities/. With files=originals each activity contributes the files it was
// imported from, unmodified; activities with none (Whoop, the app) get a
// generated FIT. The archive is written straight to the response.
func (a *API) exportRange(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	from, to := q.Get("from"), q.Get("to")
	if !validDate(from) || !validDate(to) {
		writeErr(w, http.StatusBadRequest, "from and to must be YYYY-MM-DD")
		return
	}
	files := q.Get("files")
	if files == "" {
		files = "generated"
	}
	if files != "generated" && files != "originals" {
		writeErr(w, http.StatusBadRequest, "files must be generated or originals")
		return
	}
	loc := a.loc()
	start, _ := time.ParseInLocation("2006-01-02", from, loc)
	endDay, _ := time.ParseInLocation("2006-01-02", to, loc)
	end := endDay.AddDate(0, 0, 1)
	if !end.After(start) {
		writeErr(w, http.StatusBadRequest, "to is before from")
		return
	}
	if end.Sub(start) > MaxExportDays*24*time.Hour {
		writeErr(w, http.StatusBadRequest, "a range can span at most five years")
		return
	}
	lo, hi := start.UnixMilli(), end.UnixMilli()-1

	acts, err := a.Store.ListActivities(ctx, store.ActivityQuery{From: max(lo, 1), To: hi})
	if err != nil {
		a.fail(w, r, err)
		return
	}
	for i, j := 0, len(acts)-1; i < j; i, j = i+1, j-1 {
		acts[i], acts[j] = acts[j], acts[i] // oldest first
	}
	bikes, err := a.bikeNames(ctx)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	sessions, err := a.Store.SessionsBetween(ctx, lo, hi)
	if err != nil {
		a.fail(w, r, err)
		return
	}

	attachment(w, "domestique-"+from+"-to-"+to+".zip", "application/zip")
	zw := zip.NewWriter(w)
	used := map[string]bool{}
	unique := func(name string) string {
		base, ext := name, ""
		if i := strings.Index(name, "."); i > 0 {
			base, ext = name[:i], name[i:]
		}
		for n := 2; used[name]; n++ {
			name = base + "-" + strconv.Itoa(n) + ext
		}
		used[name] = true
		return name
	}
	create := func(name string, at time.Time) (io.Writer, error) {
		return zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: at})
	}
	abort := func(err error) {
		// Headers are gone: break the connection so the browser reports a
		// failed download instead of saving a truncated zip.
		if !errors.Is(err, context.Canceled) {
			a.Log.Error("range export", "err", err.Error())
		}
		panic(http.ErrAbortHandler)
	}

	rows := make([]export.ActivityRow, 0, len(acts))
	for _, act := range acts {
		if err := ctx.Err(); err != nil {
			abort(err)
		}
		srcs, err := a.Store.SourcesFor(ctx, act.ID)
		if err != nil {
			abort(err)
		}
		row := export.ActivityRow{Activity: act, Bike: bikes[act.ID]}
		_ = json.Unmarshal(act.Metrics, &row.Metrics)
		seen := map[string]bool{}
		for _, s := range srcs {
			if !seen[s.Source] {
				seen[s.Source] = true
				row.Sources = append(row.Sources, s.Source)
			}
		}
		at := time.UnixMilli(act.StartAt)
		if files == "originals" {
			for _, s := range srcs {
				if s.RawSHA256 == nil {
					continue
				}
				raw, err := a.Store.GetRaw(ctx, *s.RawSHA256)
				if err != nil {
					continue
				}
				f, err := a.openRaw(raw)
				if err != nil {
					a.Log.Warn("range export: original missing", "sha", raw.SHA256, "err", err.Error())
					continue
				}
				ext := rawExt(raw.Path)
				name := unique("activities/" + strings.TrimSuffix(a.fileName(act.StartAt, act.Name, act.Sport, "x"), ".x") +
					"-" + export.Slug(s.Source) + "." + ext)
				zf, err := create(name, at)
				if err == nil {
					_, err = io.Copy(zf, f)
				}
				f.Close()
				if err != nil {
					abort(err)
				}
				row.Files = append(row.Files, name)
			}
		}
		if len(row.Files) == 0 {
			data, err := a.activityFIT(ctx, act)
			if err == nil {
				name := unique("activities/" + a.fileName(act.StartAt, act.Name, act.Sport, "fit"))
				zf, err := create(name, at)
				if err == nil {
					_, err = zf.Write(data)
				}
				if err != nil {
					abort(err)
				}
				row.Files = append(row.Files, name)
			} else if !errors.Is(err, errNoSamples) {
				abort(err)
			}
		}
		rows = append(rows, row)
	}

	zf, err := create("activities.csv", time.Now())
	if err != nil {
		abort(err)
	}
	ac, err := export.NewActivitiesCSV(zf, loc)
	if err != nil {
		abort(err)
	}
	for _, row := range rows {
		if err := ac.Row(row); err != nil {
			abort(err)
		}
	}
	if err := ac.Close(); err != nil {
		abort(err)
	}

	zf, err = create("sets.csv", time.Now())
	if err != nil {
		abort(err)
	}
	sc, err := export.NewSetsCSV(zf, loc)
	if err != nil {
		abort(err)
	}
	for _, s := range sessions {
		sets, err := a.Store.SessionSets(ctx, s.ID)
		if err != nil {
			abort(err)
		}
		if err := sc.Session(s, sets); err != nil {
			abort(err)
		}
	}
	if err := sc.Close(); err != nil {
		abort(err)
	}
	if err := zw.Close(); err != nil {
		abort(err)
	}
}

// bikeNames maps each ride to the name of the bike it counts towards.
func (a *API) bikeNames(ctx context.Context) (map[string]string, error) {
	bikes, err := a.Store.ListBikes(ctx)
	if err != nil || len(bikes) == 0 {
		return map[string]string{}, err
	}
	rides, err := a.Store.GearRides(ctx)
	if err != nil {
		return nil, err
	}
	over, err := a.Store.BikeOverrides(ctx)
	if err != nil {
		return nil, err
	}
	name := map[string]string{}
	for _, b := range bikes {
		name[b.ID] = b.Name
	}
	out := map[string]string{}
	for _, r := range rides {
		if as := gear.Assign(r, bikes, over); as.BikeID != "" {
			out[r.ID] = name[as.BikeID]
		}
	}
	return out, nil
}
