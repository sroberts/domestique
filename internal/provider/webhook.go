package provider

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// Webhook bookkeeping shared by providers. A webhook only says something
// changed: the provider's own authenticated sync fetches what. That keeps a
// forged or replayed webhook from ever supplying data, only from asking for a
// sync, and batching caps how often it can do that.

// WebhookSyncDelay gathers a burst of events into one sync.
var WebhookSyncDelay = 10 * time.Second

type webhookState struct {
	mu      sync.Mutex
	seen    map[string]time.Time // delivery key -> when, for dropping redeliveries
	pending bool                 // a sync is already scheduled
	token   string               // shared webhook token (Wahoo)
}

// Seen reports whether this delivery key was handled in the last day, and
// remembers it if not.
func (c *Client) Seen(key string, now time.Time) bool {
	if key == "" {
		return false
	}
	s := &c.hooks
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seen == nil {
		s.seen = map[string]time.Time{}
	}
	if t, ok := s.seen[key]; ok && now.Sub(t) < 24*time.Hour {
		return true
	}
	if len(s.seen) > 2000 {
		for k, t := range s.seen {
			if now.Sub(t) >= 24*time.Hour {
				delete(s.seen, k)
			}
		}
	}
	s.seen[key] = now
	return false
}

// ScheduleSync wakes the sync loop after WebhookSyncDelay, once however many
// events arrive meanwhile.
func (c *Client) ScheduleSync() {
	s := &c.hooks
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
		c.SyncNow()
	})
}

// MarkWebhook records that a verified webhook arrived, for Settings.
func (c *Client) MarkWebhook(ctx context.Context) {
	_ = c.Store.SetSyncState(ctx, c.cfg.Name+":last_webhook", strconv.FormatInt(time.Now().UnixMilli(), 10))
}

// WebhookToken is the shared secret a provider sends with each webhook ("" if
// it doesn't use one).
func (c *Client) WebhookToken() string {
	c.hooks.mu.Lock()
	defer c.hooks.mu.Unlock()
	return c.hooks.token
}

// CheckWebhookToken compares in constant time; an unset token matches nothing.
func (c *Client) CheckWebhookToken(got string) bool {
	want := c.WebhookToken()
	return want != "" && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// LoadWebhookToken reads the provider's webhook token from the secrets
// directory, making one if there isn't one yet (or if regenerate is set).
func (c *Client) LoadWebhookToken(dir string, regenerate bool) error {
	p := filepath.Join(dir, c.cfg.Name+"-webhook-token")
	tok := ""
	if !regenerate {
		v, err := readSecretFile(c.cfg.Name+" webhook token", p)
		switch {
		case err == nil:
			tok = v
		case !errors.Is(err, os.ErrNotExist):
			return err
		}
	}
	if tok == "" {
		b := make([]byte, 24)
		if _, err := rand.Read(b); err != nil {
			return err
		}
		tok = hex.EncodeToString(b)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		if err := writePrivate(p, tok); err != nil {
			return err
		}
	}
	c.hooks.mu.Lock()
	c.hooks.token = tok
	c.hooks.mu.Unlock()
	return nil
}
