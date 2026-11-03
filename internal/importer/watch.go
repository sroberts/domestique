package importer

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"domestique/internal/ingest"
)

// Watcher imports new activity files from folders: Wahoo's Dropbox auto-sync
// folder, Zwift's Activities folder, or anywhere FIT files land. It polls
// rather than relying on filesystem events, which Dropbox and network drives
// deliver unreliably.
type Watcher struct {
	In       *ingest.Ingester
	Dirs     []string
	Interval time.Duration
	// After runs once after any scan that imported something (load recalc).
	After func(ctx context.Context)
	// Immediate skips the settle delay, for one-off imports from the CLI.
	Immediate bool
}

var activityExts = []string{".fit", ".fit.gz", ".gpx", ".gpx.gz", ".tcx", ".tcx.gz"}

func isActivityFile(name string) bool {
	l := strings.ToLower(name)
	for _, e := range activityExts {
		if strings.HasSuffix(l, e) {
			return true
		}
	}
	return false
}

// Run scans until ctx is cancelled.
func (w *Watcher) Run(ctx context.Context) {
	if len(w.Dirs) == 0 {
		return
	}
	interval := w.Interval
	if interval <= 0 {
		interval = time.Minute
	}
	for {
		if n := w.Scan(ctx); n > 0 && w.After != nil {
			w.After(ctx)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// Scan imports anything new and returns how many activities it added.
func (w *Watcher) Scan(ctx context.Context) int {
	added := 0
	for _, dir := range w.Dirs {
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || ctx.Err() != nil {
				return nil
			}
			if d.IsDir() || !isActivityFile(d.Name()) {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			// A file still being written by Dropbox or Zwift settles first.
			if !w.Immediate && time.Since(info.ModTime()) < 10*time.Second {
				return nil
			}
			key := "watch:" + p
			sig := strconv.FormatInt(info.Size(), 10) + ":" + strconv.FormatInt(info.ModTime().Unix(), 10)
			if prev, err := w.In.Store.SyncState(ctx, key); err == nil && prev == sig {
				return nil
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return nil
			}
			res, err := w.In.Ingest(ctx, p, data, "watch:"+dir, ingest.Hint{Source: "watch"})
			if err != nil {
				w.In.Log.Warn("watch: import failed", "file", p, "err", err.Error())
			} else if !res.Duplicate {
				added++
			}
			// Remember the file even if it failed, so a bad file isn't retried every minute.
			_ = w.In.Store.SetSyncState(ctx, key, sig)
			return nil
		})
	}
	return added
}

// DirsFromEnv splits DOMESTIQUE_WATCH_DIRS on the OS list separator, expanding ~.
func DirsFromEnv(v string) []string {
	var out []string
	home, _ := os.UserHomeDir()
	for _, d := range filepath.SplitList(v) {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		if strings.HasPrefix(d, "~/") && home != "" {
			d = filepath.Join(home, d[2:])
		}
		out = append(out, d)
	}
	return out
}
