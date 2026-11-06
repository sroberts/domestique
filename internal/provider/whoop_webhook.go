package provider

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Whoop webhooks (https://developer.whoop.com/docs/developing/webhooks/).
// They say what changed, not what it changed to, so an update triggers a sync
// and a deletion removes the record here. Polling carries on as the
// reconciliation Whoop recommends, since a webhook can be missed.

// WebhookMaxSkew bounds how old (or how far ahead) a signed timestamp may be.
// Whoop retries a failed delivery for about an hour; replaying a genuine
// event within the window only repeats work that's already idempotent.
const WebhookMaxSkew = 2 * time.Hour

// ErrBadSignature is returned for a webhook that isn't from Whoop for this app.
var ErrBadSignature = errors.New("bad signature")

// WhoopEvent is a webhook body (model version v2: ids are UUIDs).
type WhoopEvent struct {
	UserID  int64  `json:"user_id"`
	ID      string `json:"id"`
	Type    string `json:"type"`
	TraceID string `json:"trace_id"`
}

// VerifyWhoopSignature checks X-WHOOP-Signature: base64(HMAC-SHA256(client
// secret, timestamp + raw body)), with the timestamp in milliseconds.
func VerifyWhoopSignature(secret, sig, ts string, body []byte, now time.Time) error {
	if secret == "" || sig == "" || ts == "" {
		return ErrBadSignature
	}
	ms, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return ErrBadSignature
	}
	if d := now.Sub(time.UnixMilli(ms)); d > WebhookMaxSkew || d < -WebhookMaxSkew {
		return ErrBadSignature
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts))
	mac.Write(body)
	want := mac.Sum(nil)
	got, err := base64.StdEncoding.DecodeString(strings.TrimSpace(sig))
	if err != nil || !hmac.Equal(got, want) {
		return ErrBadSignature
	}
	return nil
}

// webhookState is the per-process bookkeeping for Whoop events.
type webhookState struct {
	mu      sync.Mutex
	seen    map[string]time.Time // trace_id -> when, for dropping redeliveries
	pending bool                 // a sync is already scheduled
}

// Seen reports whether this trace ID was handled in the last day, and
// remembers it if not.
func (w *Whoop) Seen(traceID string, now time.Time) bool {
	if traceID == "" {
		return false
	}
	s := &w.hooks
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seen == nil {
		s.seen = map[string]time.Time{}
	}
	if t, ok := s.seen[traceID]; ok && now.Sub(t) < 24*time.Hour {
		return true
	}
	if len(s.seen) > 2000 {
		for k, t := range s.seen {
			if now.Sub(t) >= 24*time.Hour {
				delete(s.seen, k)
			}
		}
	}
	s.seen[traceID] = now
	return false
}

// WebhookSyncDelay gathers a burst of events (a sleep brings sleep and
// recovery together) into one sync.
var WebhookSyncDelay = 10 * time.Second

// HandleEvent applies one verified event.
func (w *Whoop) HandleEvent(ctx context.Context, ev WhoopEvent) error {
	_ = w.C.Store.SetSyncState(ctx, "whoop:last_webhook", strconv.FormatInt(time.Now().UnixMilli(), 10))
	switch ev.Type {
	case "workout.updated", "sleep.updated", "recovery.updated":
		w.scheduleSync()
		return nil
	case "workout.deleted":
		if ev.ID == "" {
			return nil
		}
		err := w.In.RemoveSource(ctx, "whoop:"+ev.ID)
		if err == nil && w.After != nil {
			w.After(ctx)
		}
		return err
	case "sleep.deleted":
		// Whoop deletes the recovery along with its sleep.
		date, err := w.C.Store.DeleteSleep(ctx, "whoop:"+ev.ID)
		if err != nil || date == "" {
			return err
		}
		return w.C.Store.DeleteRecovery(ctx, date, "whoop")
	case "recovery.deleted":
		// v2 recovery events carry the sleep's UUID.
		date, err := w.C.Store.SleepDate(ctx, "whoop:"+ev.ID)
		if err != nil || date == "" {
			return err
		}
		return w.C.Store.DeleteRecovery(ctx, date, "whoop")
	}
	return nil // an event type we don't use
}

func (w *Whoop) scheduleSync() {
	s := &w.hooks
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending {
		return
	}
	s.pending = true
	time.AfterFunc(WebhookSyncDelay, func() {
		s.mu.Lock()
		s.pending = false
		s.mu.Unlock()
		w.C.SyncNow()
	})
}
