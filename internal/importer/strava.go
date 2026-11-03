// Package importer brings activities in from outside: a Strava bulk export,
// watched folders of FIT files, and (later) the Whoop and Wahoo APIs.
package importer

import (
	"archive/zip"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
	"time"

	"domestique/internal/ingest"
)

// Progress is reported as an import runs.
type Progress struct {
	Total, Done, Imported, Merged, Duplicates, Failed int
	Last                                              string
}

// StravaExport imports a Strava bulk-export zip ("Download your archive"):
// activities.csv carries the athlete's titles and types, and activities/
// holds the original uploads as FIT, GPX or TCX, often gzipped.
func StravaExport(ctx context.Context, in *ingest.Ingester, zipPath string, report func(Progress)) (Progress, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return Progress{}, fmt.Errorf("open export: %w", err)
	}
	defer zr.Close()

	files := map[string]*zip.File{}
	var csvFile *zip.File
	for _, f := range zr.File {
		name := strings.TrimPrefix(f.Name, "./")
		files[name] = f
		if path.Base(name) == "activities.csv" && csvFile == nil {
			csvFile = f
		}
	}
	if csvFile == nil {
		return Progress{}, errors.New("activities.csv not found: is this a Strava bulk export?")
	}
	prefix := strings.TrimSuffix(strings.TrimPrefix(csvFile.Name, "./"), "activities.csv")

	rc, err := csvFile.Open()
	if err != nil {
		return Progress{}, err
	}
	rows, err := csv.NewReader(rc).ReadAll()
	rc.Close()
	if err != nil {
		return Progress{}, fmt.Errorf("read activities.csv: %w", err)
	}
	if len(rows) < 2 {
		return Progress{}, errors.New("activities.csv is empty")
	}
	col := columns(rows[0])
	need := []string{"Activity ID", "Activity Date", "Activity Type"}
	for _, c := range need {
		if _, ok := col.first[c]; !ok {
			return Progress{}, fmt.Errorf("activities.csv has no %q column", c)
		}
	}

	p := Progress{Total: len(rows) - 1}
	for _, row := range rows[1:] {
		if err := ctx.Err(); err != nil {
			return p, err
		}
		p.Done++
		get := func(name string) string { return col.get(row, name, false) }
		id := get("Activity ID")
		h := ingest.Hint{
			Source:      "strava",
			ExternalID:  id,
			Name:        get("Activity Name"),
			Sport:       get("Activity Type"),
			Description: get("Activity Description"),
		}
		p.Last = h.Name
		var res ingest.Result
		if fn := get("Filename"); fn != "" {
			f := files[prefix+fn]
			if f == nil {
				f = files[fn]
			}
			if f == nil {
				p.Failed++
				in.Log.Warn("strava export: file missing", "id", id, "file", fn)
				continue
			}
			data, err := readZip(f)
			if err == nil {
				res, err = in.Ingest(ctx, fn, data, "strava-export", h)
			}
			if err != nil {
				p.Failed++
				in.Log.Warn("strava export: import failed", "id", id, "file", fn, "err", err.Error())
				continue
			}
		} else {
			// No upload (a manual entry, a lift logged by hand): keep the record.
			start, err := parseStravaDate(get("Activity Date"))
			if err != nil {
				p.Failed++
				continue
			}
			elapsed, _ := strconv.ParseFloat(col.get(row, "Elapsed Time", false), 64)
			dist, _ := strconv.ParseFloat(col.get(row, "Distance", true), 64) // the later column is metres
			res, err = in.AddManual(ctx, "strava:"+id, start, int(elapsed), dist, h, nil)
			if err != nil {
				p.Failed++
				continue
			}
		}
		switch {
		case res.Duplicate:
			p.Duplicates++
		case res.Merged:
			p.Merged++
		default:
			p.Imported++
		}
		if report != nil && p.Done%25 == 0 {
			report(p)
		}
	}
	return p, nil
}

type cols struct {
	first map[string]int
	last  map[string]int
}

// Strava's CSV repeats some headers (Distance appears in display units and
// again in metres), so keep both the first and last position of each.
func columns(header []string) cols {
	c := cols{first: map[string]int{}, last: map[string]int{}}
	for i, h := range header {
		h = strings.TrimSpace(strings.TrimPrefix(h, "\ufeff"))
		if _, ok := c.first[h]; !ok {
			c.first[h] = i
		}
		c.last[h] = i
	}
	return c
}

func (c cols) get(row []string, name string, last bool) string {
	m := c.first
	if last {
		m = c.last
	}
	i, ok := m[name]
	if !ok || i >= len(row) {
		return ""
	}
	return strings.TrimSpace(row[i])
}

func readZip(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, 256<<20))
}

// parseStravaDate reads "Jan 2, 2021, 3:04:05 PM" (UTC) and older variants.
func parseStravaDate(s string) (time.Time, error) {
	for _, layout := range []string{"Jan 2, 2006, 3:04:05 PM", "2 Jan 2006, 15:04:05", "2006-01-02 15:04:05", time.RFC3339} {
		if t, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognised date %q", s)
}
