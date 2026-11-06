package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"domestique/internal/auth"
	"domestique/internal/provider"
)

// Whoop webhooks arrive at a public path (outpost.toml public_paths), so the
// gateway lets them through without a login; the HMAC signature made with
// the app's client secret is what proves they're from Whoop.

func (a *API) registerWebhooks(mux *http.ServeMux) {
	mux.HandleFunc("POST "+auth.WhoopWebhookPath, a.whoopWebhook)
	mux.HandleFunc("POST "+auth.WahooWebhookPath, a.wahooWebhook)
	mux.HandleFunc("POST /v1/connections/wahoo/webhook-token", a.regenerateWahooToken)
}

func (a *API) whoopWebhook(w http.ResponseWriter, r *http.Request) {
	c := a.Providers["whoop"]
	if c == nil || a.Whoop == nil || !c.Config().Configured() {
		// Not set up: nothing could verify it. 404 so Whoop stops retrying soon.
		writeErr(w, http.StatusNotFound, "whoop isn't set up")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil {
		writeErr(w, http.StatusRequestEntityTooLarge, "body too large")
		return
	}
	now := time.Now()
	if err := provider.VerifyWhoopSignature(c.Config().ClientSecret, r.Header.Get("X-WHOOP-Signature"),
		r.Header.Get("X-WHOOP-Signature-Timestamp"), body, now); err != nil {
		a.Log.Warn("whoop webhook rejected", "err", err.Error(), "remote", r.RemoteAddr)
		writeErr(w, http.StatusUnauthorized, "bad signature")
		return
	}
	var ev provider.WhoopEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		// Signed but unreadable: a model version we don't speak (v1 has
		// integer ids). Accept it so Whoop doesn't retry; polling covers it.
		var v1 struct {
			ID   json.Number `json:"id"`
			Type string      `json:"type"`
		}
		if json.Unmarshal(body, &v1) == nil {
			a.Log.Warn("whoop webhook in model version v1; set the webhook to v2", "type", v1.Type)
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// Answer within Whoop's one second; the work happens after.
	w.WriteHeader(http.StatusNoContent)
	if c.Seen(ev.TraceID, now) {
		return
	}
	ctx := a.background
	if ctx == nil {
		ctx = context.Background()
	}
	go func() {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		if err := a.Whoop.HandleEvent(ctx, ev); err != nil && !errors.Is(err, context.Canceled) {
			a.Log.Warn("whoop webhook", "type", ev.Type, "err", err.Error())
		}
	}()
}

// wahooWebhook takes Wahoo's workout_summary webhooks
// (https://cloud-api.wahooligan.com/#webhooks). Wahoo proves itself only by
// echoing the webhook_token set in its developer portal, so the payload is
// used as a nudge and nothing more: its FIT file URL is never fetched. The
// regular Wahoo sync, over the authenticated API, picks the workout up.
func (a *API) wahooWebhook(w http.ResponseWriter, r *http.Request) {
	c := a.Providers["wahoo"]
	if c == nil || !c.Config().Configured() {
		writeErr(w, http.StatusNotFound, "wahoo isn't set up")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 256<<10))
	if err != nil {
		writeErr(w, http.StatusRequestEntityTooLarge, "body too large")
		return
	}
	var ev struct {
		EventType      string `json:"event_type"`
		WebhookToken   string `json:"webhook_token"`
		WorkoutSummary struct {
			ID        json.Number `json:"id"`
			UpdatedAt string      `json:"updated_at"`
		} `json:"workout_summary"`
	}
	if err := json.Unmarshal(body, &ev); err != nil || !c.CheckWebhookToken(ev.WebhookToken) {
		a.Log.Warn("wahoo webhook rejected", "remote", r.RemoteAddr)
		writeErr(w, http.StatusUnauthorized, "bad token")
		return
	}
	// Wahoo wants a 200; anything else is retried for three days.
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	ctx := a.background
	if ctx == nil {
		ctx = context.Background()
	}
	c.MarkWebhook(ctx)
	if ev.EventType != "workout_summary" {
		return
	}
	// Without an id there is no delivery key: a constant "@" would swallow
	// every such event for a day.
	if id := ev.WorkoutSummary.ID.String(); id != "" && c.Seen(id+"@"+ev.WorkoutSummary.UpdatedAt, time.Now()) {
		return
	}
	c.ScheduleSync()
}

// regenerateWahooToken replaces the webhook token, for when it may have
// leaked; the new one then has to be entered in Wahoo's portal.
func (a *API) regenerateWahooToken(w http.ResponseWriter, r *http.Request) {
	c := a.Providers["wahoo"]
	if c == nil || a.Ingest == nil {
		writeErr(w, http.StatusNotFound, "unknown provider")
		return
	}
	if err := c.LoadWebhookToken(provider.SecretsDir(a.Ingest.StorageDir), true); err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, c.Status(r.Context()))
}
