// Domestique: a self-hosted Outpost service that imports and serves workouts.
//
// Outpost contract (outpost-service-spec.md): one loopback port in plain HTTP,
// a cheap /up healthcheck that is 503 until migrations finish, configuration
// from env and flags, all state under OUTPOST_STORAGE_DIR, non-zero exit on
// failure, prompt SIGTERM shutdown, JSON logs on stdout, no daemonizing.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"domestique/internal/api"
	"domestique/internal/auth"
	"domestique/internal/demo"
	"domestique/internal/importer"
	"domestique/internal/ingest"
	"domestique/internal/load"
	"domestique/internal/mcp"
	"domestique/internal/provider"
	"domestique/internal/seed"
	"domestique/internal/store"
	"domestique/internal/webui"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "import" {
		if err := runImport(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "import:", err)
			os.Exit(1)
		}
		return
	}
	if err := run(); err != nil {
		slog.Error("fatal", "err", err.Error())
		os.Exit(1)
	}
}

func run() error {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(log)

	port := flag.Int("port", envInt("OUTPOST_PORT", 0), "TCP port to listen on (or OUTPOST_PORT)")
	flag.Parse()
	if *port <= 0 || *port > 65535 {
		return errors.New("no port: pass --port or set OUTPOST_PORT")
	}
	storage := os.Getenv("OUTPOST_STORAGE_DIR")
	if storage == "" {
		return errors.New("OUTPOST_STORAGE_DIR is not set")
	}
	maxHR := envInt("DOMESTIQUE_MAX_HR", 190)
	if maxHR < 120 || maxHR > 230 {
		return fmt.Errorf("DOMESTIQUE_MAX_HR %d is outside 120-230", maxHR)
	}

	loc := time.Local
	if tz := os.Getenv("DOMESTIQUE_TZ"); tz != "" {
		l, err := time.LoadLocation(tz)
		if err != nil {
			return fmt.Errorf("DOMESTIQUE_TZ: %w", err)
		}
		loc = l
	}

	// Provider credentials are read now so a bad secret path fails loudly at start.
	whoopCfg, err := provider.LoadConfig(provider.WhoopConfig, "DOMESTIQUE_WHOOP")
	if err != nil {
		return err
	}
	wahooCfg, err := provider.LoadConfig(provider.WahooConfig, "DOMESTIQUE_WAHOO")
	if err != nil {
		return err
	}

	var ready atomic.Bool
	gw := auth.FromEnv(log)
	app := &api.API{MaxHR: maxHR, Log: log, Loc: loc, Uploads: &api.Uploads{}, Providers: map[string]*provider.Client{},
		Gateway: gw.Enabled}

	appMux := http.NewServeMux()
	app.Register(appMux)
	appMux.Handle("/mcp", &mcp.Server{API: appMux, Version: version})
	appMux.Handle("/", webui.Handler())
	guarded := gw.Middleware(appMux)

	root := http.NewServeMux()
	root.HandleFunc("GET /up", func(w http.ResponseWriter, r *http.Request) {
		// No I/O, no locks: this runs every three seconds forever.
		if !ready.Load() {
			http.Error(w, "starting", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	// /.outpost/ belongs to Outpost's login gateway (spec §11). Without this
	// the PWA's deep-link fallback would answer it with index.html.
	root.Handle("/.outpost/", http.NotFoundHandler())
	root.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !ready.Load() {
			http.Error(w, "starting", http.StatusServiceUnavailable)
			return
		}
		guarded.ServeHTTP(w, r)
	}))

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	srv := &http.Server{
		Handler:           logRequests(log, root),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	var workers sync.WaitGroup

	errc := make(chan error, 2)
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- fmt.Errorf("serve: %w", err)
		}
	}()

	// Slow startup work happens while /up answers 503.
	go func() {
		s, err := store.Open(ctx, storage)
		if err != nil {
			errc <- fmt.Errorf("open store: %w", err)
			return
		}
		if err := seed.Templates(ctx, s); err != nil {
			s.Close()
			errc <- err
			return
		}
		app.Store = s
		// Upload spools from a run that was stopped mid-import.
		_ = os.RemoveAll(filepath.Join(storage, "tmp"))
		ing := &ingest.Ingester{Store: s, StorageDir: storage, Log: log, Loc: loc}
		app.Ingest = ing
		app.SetBackground(ctx)
		if os.Getenv("DOMESTIQUE_DEMO") == "1" {
			delay := time.Duration(envInt("DOMESTIQUE_DEMO_HR_DELAY", 15)) * time.Second
			app.Demo = &demo.Demo{Store: s, MaxHR: maxHR, HRDelay: delay}
		}

		// Training load is recomputed after imports, debounced so a batch of
		// files triggers one pass.
		kick := make(chan struct{}, 1)
		app.Recalc = func() {
			select {
			case kick <- struct{}{}:
			default:
			}
		}
		workers.Add(1)
		go func() {
			defer workers.Done()
			remerge(ctx, s, ing, log)
			for {
				if _, err := load.Recalc(ctx, s, maxHR); err != nil && ctx.Err() == nil {
					log.Warn("load recalc", "err", err.Error())
				}
				select {
				case <-ctx.Done():
					return
				case <-kick:
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(2 * time.Second):
				}
			}
		}()
		after := func(context.Context) { app.Recalc() }

		// Credentials from the manifest win; otherwise any saved in Settings.
		// A saved pair that can't be read is logged rather than fatal, so it
		// can be re-entered from Settings.
		saved := func(cfg provider.Config) (provider.Config, string) {
			if cfg.Configured() {
				return cfg, provider.FromManifest
			}
			id, secret, err := provider.LoadSaved(provider.SecretsDir(storage), cfg.Name)
			if err != nil {
				log.Error("saved credentials unreadable; enter them again in Settings", "provider", cfg.Name, "err", err.Error())
				return cfg, provider.FromNone
			}
			cfg.ClientID, cfg.ClientSecret = id, secret
			return cfg, provider.FromSettings
		}
		whoopCfg, whoopFrom := saved(whoopCfg)
		wahooCfg, wahooFrom := saved(wahooCfg)
		whoop := provider.NewClient(whoopCfg, whoopFrom, s, log)
		wahoo := provider.NewClient(wahooCfg, wahooFrom, s, log)
		app.Providers["whoop"], app.Providers["wahoo"] = whoop, wahoo
		// Wahoo webhooks carry a shared token; make one on first run.
		if err := wahoo.LoadWebhookToken(provider.SecretsDir(storage), false); err != nil {
			log.Error("wahoo webhook token", "err", err.Error())
		}
		w1 := &provider.Whoop{C: whoop, In: ing, Loc: loc, After: after}
		app.Whoop = w1
		w2 := &provider.Wahoo{C: wahoo, In: ing}
		workers.Add(2)
		go func() { defer workers.Done(); whoop.Loop(ctx, 30*time.Minute, w1.Sync, after) }()
		go func() { defer workers.Done(); wahoo.Loop(ctx, 15*time.Minute, w2.Sync, after) }()

		watchDirs := importer.DirsFromEnv(os.Getenv("DOMESTIQUE_WATCH_DIRS"))
		if len(watchDirs) > 0 {
			watcher := &importer.Watcher{In: ing, Dirs: watchDirs, Interval: time.Minute, After: after}
			workers.Add(1)
			go func() { defer workers.Done(); watcher.Run(ctx) }()
		}

		ready.Store(true)
		log.Info("ready", "addr", ln.Addr().String(), "storage", storage, "version", version,
			"gateway", gw.Enabled, "demo", app.Demo != nil, "whoop", whoopCfg.Configured(),
			"wahoo", wahooCfg.Configured(), "watch", watchDirs, "tz", loc.String())
	}()

	var runErr error
	select {
	case <-ctx.Done():
		log.Info("shutting down")
	case runErr = <-errc:
	}

	// Leave headroom inside Outpost's 10s SIGKILL deadline.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Warn("shutdown", "err", err.Error())
	}
	stop() // cancels importers and pollers
	done := make(chan struct{})
	go func() { workers.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		log.Warn("background work still running at shutdown")
	}
	if ready.Load() { // ready's atomic store publishes app.Store to this goroutine
		if err := app.Store.Close(); err != nil {
			log.Warn("close store", "err", err.Error())
		}
	}
	if runErr != nil {
		return runErr
	}
	log.Info("stopped")
	return nil
}

// version is set at release time with -ldflags "-X main.version=...".
var version = "dev"

// runImport is `domestique import <file|dir|export.zip>...`. It writes to the
// same storage directory as the running service (SQLite allows both), so the
// athlete can load years of history from a terminal on the Mac Mini:
//
//	OUTPOST_STORAGE_DIR=~/.outpost/data/domestique domestique import ~/Downloads/export_123.zip
func runImport(args []string) error {
	storage := os.Getenv("OUTPOST_STORAGE_DIR")
	if storage == "" {
		return errors.New("set OUTPOST_STORAGE_DIR to the service's storage directory")
	}
	if len(args) == 0 {
		return errors.New("usage: domestique import <file.fit|folder|strava-export.zip> [more]")
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	st, err := store.Open(ctx, storage)
	if err != nil {
		return err
	}
	defer st.Close()
	ing := &ingest.Ingester{Store: st, StorageDir: storage, Log: log}
	if tz := os.Getenv("DOMESTIQUE_TZ"); tz != "" {
		l, err := time.LoadLocation(tz)
		if err != nil {
			return fmt.Errorf("DOMESTIQUE_TZ: %w", err)
		}
		ing.Loc = l
	}
	for _, p := range args {
		info, err := os.Stat(p)
		if err != nil {
			return err
		}
		switch {
		case info.IsDir():
			n := (&importer.Watcher{In: ing, Dirs: []string{p}, Immediate: true}).Scan(ctx)
			fmt.Printf("%s: %d new activities\n", p, n)
		case strings.HasSuffix(strings.ToLower(p), ".zip"):
			prog, err := importer.StravaExport(ctx, ing, p, func(pr importer.Progress) {
				fmt.Printf("\r%d/%d  %-50.50s", pr.Done, pr.Total, pr.Last)
			})
			fmt.Println()
			if err != nil {
				return err
			}
			fmt.Printf("%s: %d new, %d merged into existing, %d already imported, %d failed\n",
				p, prog.Imported, prog.Merged, prog.Duplicates, prog.Failed)
		default:
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			res, err := ing.Ingest(ctx, p, data, "cli", ingest.Hint{Source: "upload"})
			if err != nil {
				return err
			}
			fmt.Printf("%s: activity %s (merged=%v duplicate=%v)\n", p, res.ActivityID, res.Merged, res.Duplicate)
		}
	}
	remerge(ctx, st, ing, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	ath, err := load.Recalc(ctx, st, envInt("DOMESTIQUE_MAX_HR", 190))
	if err != nil {
		return err
	}
	fmt.Printf("FTP %d W (%s), max HR %d (%s)\n", ath.FTP, ath.FTPSource, ath.MaxHR, ath.MaxHRSource)
	return nil
}

func envInt(key string, fallback int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil {
		return v
	}
	return fallback
}

type statusWriter struct {
	http.ResponseWriter
	code int
}

func (w *statusWriter) WriteHeader(code int) {
	w.code = code
	w.ResponseWriter.WriteHeader(code)
}

// logRequests logs API calls and errors, but not healthchecks or static assets.
func logRequests(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(sw, r)
		if r.URL.Path == "/up" {
			return
		}
		if sw.code >= 400 || len(r.URL.Path) > 3 && r.URL.Path[:4] == "/v1/" {
			log.Info("request", "method", r.Method, "path", r.URL.Path, "status", sw.code,
				"ms", time.Since(start).Milliseconds())
		}
	})
}

// remerge folds together recordings of one workout that an older, stricter
// matching rule left apart. It runs once per rule.
func remerge(ctx context.Context, s *store.Store, ing *ingest.Ingester, log *slog.Logger) {
	if v, err := s.SyncState(ctx, "merge:rule"); err == nil && v == ingest.MergeRule {
		return
	}
	changed, err := ing.Remerge(ctx)
	if err != nil {
		if ctx.Err() == nil {
			log.Warn("re-merge", "err", err.Error())
		}
		return
	}
	if err := s.SetSyncState(ctx, "merge:rule", ingest.MergeRule); err != nil {
		log.Warn("re-merge", "err", err.Error())
	}
	log.Info("re-merged recordings", "rule", ingest.MergeRule, "activities", len(changed))
}
