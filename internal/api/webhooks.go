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
	if a.Whoop.Seen(ev.TraceID, now) {
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
