package coach

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// KV is where finished work is kept, so a restart doesn't pay for it twice.
type KV interface {
	SyncState(ctx context.Context, key string) (string, error)
	SetSyncState(ctx context.Context, key, value string) error
}

// Writer runs model work in the background and keeps the results. A request
// never waits on a model: it gets what's kept, or "pending" while the work
// runs, and shows its rule-based answer meanwhile.
type Writer struct {
	AI  *AI
	KV  KV
	Log *slog.Logger
	// Background outlives requests; cancelled on shutdown.
	Background context.Context

	mu       sync.Mutex
	inflight map[string]bool
	failed   map[string]time.Time // fingerprint -> when it last failed
}

// retryAfter is how long failed work waits before another try: hosted models
// cost money, and an unavailable one stays so for a while.
const retryAfter = 15 * time.Minute

type kept struct {
	FP    string          `json:"fp"`
	Value json.RawMessage `json:"value"`
}

// Cached returns the result kept under key if it was made from fp.
func (w *Writer) Cached(ctx context.Context, key, fp string, out any) bool {
	v, err := w.KV.SyncState(ctx, key)
	if err != nil || v == "" {
		return false
	}
	var k kept
	if json.Unmarshal([]byte(v), &k) != nil || k.FP != fp {
		return false
	}
	return json.Unmarshal(k.Value, out) == nil
}

// Keep stores a result under key, made from fp.
func (w *Writer) Keep(ctx context.Context, key, fp string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b, _ := json.Marshal(kept{FP: fp, Value: raw})
	return w.KV.SetSyncState(ctx, key, string(b))
}

// Do fills out with the result kept under key for fp, or starts run in the
// background and reports pending. After a failure the same work isn't
// retried for retryAfter, and neither ok nor pending is reported.
func (w *Writer) Do(ctx context.Context, key, fp string, out any, run func(context.Context) (any, error)) (ok, pending bool) {
	if w.Cached(ctx, key, fp, out) {
		return true, false
	}
	job := key + "@" + fp
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.inflight == nil {
		w.inflight, w.failed = map[string]bool{}, map[string]time.Time{}
	}
	if w.inflight[job] {
		return false, true
	}
	if t, bad := w.failed[job]; bad && time.Since(t) < retryAfter {
		return false, false
	}
	w.inflight[job] = true
	bg := w.Background
	if bg == nil {
		bg = context.Background()
	}
	go func() {
		start := time.Now()
		v, err := run(bg)
		if err == nil {
			// Keep it before clearing inflight, so no request in between
			// starts the same work again.
			if err = w.Keep(bg, key, fp, v); err != nil {
				err = fmt.Errorf("keep: %w", err)
			}
		}
		w.mu.Lock()
		delete(w.inflight, job)
		if err != nil {
			w.failed[job] = time.Now()
		} else {
			delete(w.failed, job)
		}
		w.mu.Unlock()
		if err != nil {
			w.LogFailure(bg, key, err)
			return
		}
		w.Log.Info("coach: written", "key", key, "took", time.Since(start).Round(time.Millisecond).String())
	}()
	return false, true
}

// LogFailure logs model work that didn't produce a result: at info when AI
// is simply off or busy (the operator's choice, not a fault), else a warning.
func (w *Writer) LogFailure(ctx context.Context, what string, err error) {
	lvl := slog.LevelWarn
	if Unavailable(err) {
		lvl = slog.LevelInfo
	}
	w.Log.Log(ctx, lvl, "coach: not written; showing the rule-based answer", "key", what, "err", err.Error())
}

// Fingerprint names a set of facts and the prompt they're used with, so new
// facts or a changed prompt make the work stale.
func Fingerprint(prompt string, facts any) string {
	b, _ := json.Marshal(facts)
	sum := sha256.Sum256(append([]byte(prompt), b...))
	return hex.EncodeToString(sum[:8])
}

// Note is a short piece of prose the LLM wrote, and which model wrote it.
type Note struct {
	Text  string `json:"text"`
	Model string `json:"model,omitempty"`
}
