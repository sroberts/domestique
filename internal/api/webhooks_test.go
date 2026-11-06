package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"domestique/internal/ingest"
	"domestique/internal/provider"
	"domestique/internal/store"
)

func whoopPost(t *testing.T, h http.Handler, secret string, body string) int {
	t.Helper()
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(ts))
	m.Write([]byte(body))
	r := httptest.NewRequest("POST", "/v1/webhooks/whoop", bytes.NewBufferString(body))
	r.Header.Set("X-WHOOP-Signature", base64.StdEncoding.EncodeToString(m.Sum(nil)))
	r.Header.Set("X-WHOOP-Signature-Timestamp", ts)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w.Code
}

func eventually(t *testing.T, what string, f func() bool) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if f() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestWhoopWebhook(t *testing.T) {
	a, h := newTestAPI(t)
	ctx := context.Background()
	a.Ingest = &ingest.Ingester{Store: a.Store, StorageDir: t.TempDir(), Log: a.Log, Loc: time.UTC}

	// Not set up: refused, nothing could verify it.
	if code := whoopPost(t, h, "s", `{}`); code != http.StatusNotFound {
		t.Fatalf("unconfigured = %d", code)
	}

	cfg := provider.WhoopConfig
	cfg.ClientID, cfg.ClientSecret = "id", "whoop-secret"
	c := provider.NewClient(cfg, provider.FromSettings, a.Store, a.Log)
	a.Providers = map[string]*provider.Client{"whoop": c}
	a.Whoop = &provider.Whoop{C: c, In: a.Ingest, Loc: time.UTC}

	// Forged: wrong secret.
	if code := whoopPost(t, h, "guess", `{"id":"x","type":"workout.deleted","trace_id":"f"}`); code != http.StatusUnauthorized {
		t.Fatalf("forged = %d", code)
	}

	// workout.deleted removes the Whoop source and its now-empty activity.
	const wid = "ecfc6a15-4661-442f-a9a4-f160dd7afae8"
	res, err := a.Ingest.AddWhoopWorkout(ctx, wid, time.Now().Add(-3*time.Hour), 3600, 0,
		ingest.Hint{Source: "whoop", ExternalID: wid, Sport: "WeightTraining"}, 120, 160, [6]int64{})
	if err != nil {
		t.Fatal(err)
	}
	if code := whoopPost(t, h, "whoop-secret", fmt.Sprintf(`{"user_id":1,"id":%q,"type":"workout.deleted","trace_id":"t1"}`, wid)); code != http.StatusNoContent {
		t.Fatalf("delete = %d", code)
	}
	eventually(t, "workout removed", func() bool {
		_, err := a.Store.GetSource(ctx, "whoop:"+wid)
		_, aerr := a.Store.GetActivity(ctx, res.ActivityID)
		return errors.Is(err, store.ErrNotFound) && errors.Is(aerr, store.ErrNotFound)
	})

	// sleep.deleted takes that night's Whoop recovery with it.
	const sid = "11111111-2222-3333-4444-555555555555"
	_ = a.Store.PutSleep(ctx, store.Sleep{ID: "whoop:" + sid, Date: "2026-11-05", StartAt: 1, EndAt: 2, AsleepS: 1, Source: "whoop"})
	_ = a.Store.PutRecovery(ctx, store.Recovery{Date: "2026-11-05", Score: 70, HRVms: 50, RestingHR: 50, Source: "whoop"})
	whoopPost(t, h, "whoop-secret", fmt.Sprintf(`{"user_id":1,"id":%q,"type":"sleep.deleted","trace_id":"t2"}`, sid))
	eventually(t, "sleep and recovery removed", func() bool {
		_, err := a.Store.GetRecovery(ctx, "2026-11-05")
		d, _ := a.Store.SleepDate(ctx, "whoop:"+sid)
		return errors.Is(err, store.ErrNotFound) && d == ""
	})

	// Updates schedule a sync; the last webhook time shows in status.
	provider.WebhookSyncDelay = time.Millisecond
	if code := whoopPost(t, h, "whoop-secret", `{"user_id":1,"id":"abc","type":"recovery.updated","trace_id":"t3"}`); code != http.StatusNoContent {
		t.Fatalf("update = %d", code)
	}
	eventually(t, "last webhook recorded", func() bool { return c.Status(ctx).LastWebhook > 0 })

	// A redelivery (same trace_id) is accepted and ignored; a v1 body too.
	if code := whoopPost(t, h, "whoop-secret", `{"user_id":1,"id":"abc","type":"recovery.updated","trace_id":"t3"}`); code != http.StatusNoContent {
		t.Fatalf("redelivery = %d", code)
	}
	if code := whoopPost(t, h, "whoop-secret", `{"user_id":1,"id":1234,"type":"sleep.updated","trace_id":"t4"}`); code != http.StatusNoContent {
		t.Fatalf("v1 body = %d", code)
	}
}

func TestWahooWebhook(t *testing.T) {
	a, h := newTestAPI(t)
	ctx := context.Background()
	dir := t.TempDir()
	a.Ingest = &ingest.Ingester{Store: a.Store, StorageDir: dir, Log: a.Log}
	post := func(body string) int {
		r := httptest.NewRequest("POST", "/v1/webhooks/wahoo", bytes.NewBufferString(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	payload := func(tok string) string {
		return fmt.Sprintf(`{"event_type":"workout_summary","webhook_token":%q,"user":{"id":1},
			"workout_summary":{"id":42,"updated_at":"2026-11-05T10:00:00.000Z","file":{"url":"http://127.0.0.1:1/evil.fit"}}}`, tok)
	}

	cfg := provider.WahooConfig
	c := provider.NewClient(cfg, provider.FromNone, a.Store, a.Log)
	a.Providers = map[string]*provider.Client{"wahoo": c}
	if code := post(payload("x")); code != http.StatusNotFound {
		t.Fatalf("unconfigured = %d", code)
	}
	c.SetCredentials("id", "secret", provider.FromSettings)
	// No token generated yet: nothing matches, not even an empty one.
	if code := post(payload("")); code != http.StatusUnauthorized {
		t.Fatalf("empty token = %d", code)
	}
	if err := c.LoadWebhookToken(provider.SecretsDir(dir), false); err != nil {
		t.Fatal(err)
	}
	tok := c.WebhookToken()
	if len(tok) < 32 {
		t.Fatalf("token %q too short", tok)
	}
	// It persists, owner-only, and loads back unchanged.
	if fi, err := os.Stat(filepath.Join(provider.SecretsDir(dir), "wahoo-webhook-token")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("token file: %v %v", fi, err)
	}
	c2 := provider.NewClient(cfg, provider.FromNone, a.Store, a.Log)
	_ = c2.LoadWebhookToken(provider.SecretsDir(dir), false)
	if c2.WebhookToken() != tok {
		t.Fatal("token changed on reload")
	}

	if code := post(payload("guess")); code != http.StatusUnauthorized {
		t.Fatalf("wrong token = %d", code)
	}
	provider.WebhookSyncDelay = time.Millisecond
	if code := post(payload(tok)); code != http.StatusOK {
		t.Fatalf("genuine = %d", code)
	}
	if c.Status(ctx).LastWebhook == 0 {
		t.Fatal("last webhook not recorded")
	}

	// Regenerating retires the old token.
	if w := do(t, h, "POST", "/v1/connections/wahoo/webhook-token", nil); w.Code != http.StatusOK {
		t.Fatalf("regenerate = %d", w.Code)
	}
	if c.WebhookToken() == tok {
		t.Fatal("token not regenerated")
	}
	if code := post(payload(tok)); code != http.StatusUnauthorized {
		t.Fatalf("old token after regenerate = %d", code)
	}
}
