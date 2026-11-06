// Package provider connects Domestique to Whoop and Wahoo over OAuth 2.0 and
// polls them for new data. Webhooks can't reach a loopback-only Outpost
// service through the login gateway, so everything is polled.
//
// Client secrets come from files named in the environment, never the
// manifest; OAuth tokens live in SQLite under OUTPOST_STORAGE_DIR so backup
// and restore carry them.
package provider

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"domestique/internal/store"
)

// Config is one provider's OAuth application.
type Config struct {
	Name         string // whoop | wahoo
	AuthURL      string
	TokenURL     string
	Scopes       []string
	ClientID     string
	ClientSecret string
}

// Configured reports whether credentials were supplied.
func (c Config) Configured() bool { return c.ClientID != "" && c.ClientSecret != "" }

// LoadConfig reads <PREFIX>_CLIENT_ID (or _CLIENT_ID_FILE) and the client
// secret from the file named by <PREFIX>_CLIENT_SECRET_FILE. The secret is
// never taken from the environment itself: the Outpost manifest is plaintext
// and its env shows up in the process table (outpost-service-spec.md §3,
// Secrets), so a secret set there is refused rather than quietly used.
func LoadConfig(c Config, prefix string) (Config, error) {
	if os.Getenv(prefix+"_CLIENT_SECRET") != "" {
		return c, fmt.Errorf("%s_CLIENT_SECRET is set in the environment; put the secret in a file readable only by you (chmod 600) and set %s_CLIENT_SECRET_FILE to its path instead", prefix, prefix)
	}
	var err error
	if c.ClientID, err = fromEnvOrFile(prefix + "_CLIENT_ID"); err != nil {
		return c, err
	}
	if c.ClientID == "" {
		return c, nil // not set up yet: the secret file needn't exist
	}
	key := prefix + "_CLIENT_SECRET_FILE"
	path := os.Getenv(key)
	if path == "" {
		return c, fmt.Errorf("%s_CLIENT_ID is set but %s isn't: name the file holding the client secret", prefix, key)
	}
	if c.ClientSecret, err = readSecretFile(key, path); err != nil {
		return c, err
	}
	return c, nil
}

// fromEnvOrFile reads a non-secret value from KEY, or from the file named by
// KEY_FILE.
func fromEnvOrFile(key string) (string, error) {
	if p := os.Getenv(key + "_FILE"); p != "" {
		return readSecretFile(key+"_FILE", p)
	}
	return strings.TrimSpace(os.Getenv(key)), nil
}

// readSecretFile reads a credential file. It must be a regular file, not
// empty, and not readable by other users: the same rule ssh applies to
// private keys, so a secret left world-readable is caught at start rather
// than leaked.
func readSecretFile(key, p string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("%s: %w", key, err)
		}
		p = home + p[1:]
	}
	fi, err := os.Stat(p)
	if err != nil {
		return "", fmt.Errorf("%s: %w", key, err)
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("%s: %s is not a regular file", key, p)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("%s: %s is readable by other users (mode %04o); run chmod 600 %s", key, p, fi.Mode().Perm(), p)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("%s: %w", key, err)
	}
	v := strings.TrimSpace(string(b))
	if v == "" {
		return "", fmt.Errorf("%s: %s is empty", key, p)
	}
	return v, nil
}

// Client performs authenticated requests, refreshing tokens as needed.
type Client struct {
	Store *store.Store
	HTTP  *http.Client
	Log   *slog.Logger

	mu     sync.Mutex
	status Status
	cfg    Config
	from   string // FromManifest | FromSettings | FromNone
}

// Config returns the provider's current OAuth application.
func (c *Client) Config() Config {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cfg
}

// CredentialsFrom says where the client credentials came from.
func (c *Client) CredentialsFrom() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.from
}

// SetCredentials replaces the client ID and secret (empty clears them) and
// wakes the sync loop so a newly set-up provider starts without a restart.
func (c *Client) SetCredentials(id, secret, from string) {
	c.mu.Lock()
	c.cfg.ClientID, c.cfg.ClientSecret, c.from = id, secret, from
	if id == "" {
		c.from = FromNone
	}
	c.mu.Unlock()
	c.SyncNow()
}

// Status is what the Settings screen shows for a provider.
type Status struct {
	Provider   string `json:"provider"`
	Configured bool   `json:"configured"`
	Connected  bool   `json:"connected"`
	LastSync   int64  `json:"last_sync"`
	// LastWebhook is when the provider last sent a verified webhook (0: never).
	LastWebhook int64  `json:"last_webhook,omitempty"`
	LastError   string `json:"last_error,omitempty"`
	Syncing     bool   `json:"syncing"`
	// Where the client ID and secret came from: "manifest", "settings" or ""
	// (none). The secret itself is never returned.
	CredentialsFrom string `json:"credentials_from"`
	ClientID        string `json:"client_id,omitempty"`
}

// NewClient makes a provider client; from says where cfg's credentials came
// from (FromManifest, FromSettings, or FromNone when there are none).
func NewClient(cfg Config, from string, st *store.Store, log *slog.Logger) *Client {
	if !cfg.Configured() {
		from = FromNone
	}
	return &Client{cfg: cfg, from: from, Store: st, HTTP: &http.Client{Timeout: 60 * time.Second}, Log: log,
		status: Status{Provider: cfg.Name}}
}

func (c *Client) Status(ctx context.Context) Status {
	c.mu.Lock()
	s := c.status
	c.mu.Unlock()
	cfg := c.Config()
	s.Configured = cfg.Configured()
	s.CredentialsFrom = c.CredentialsFrom()
	s.ClientID = cfg.ClientID
	_, err := c.Store.GetToken(ctx, c.cfg.Name)
	s.Connected = err == nil
	if v, err := c.Store.SyncState(ctx, c.cfg.Name+":last_sync"); err == nil {
		s.LastSync, _ = strconv.ParseInt(v, 10, 64)
	}
	if v, err := c.Store.SyncState(ctx, c.cfg.Name+":last_webhook"); err == nil {
		s.LastWebhook, _ = strconv.ParseInt(v, 10, 64)
	}
	return s
}

func (c *Client) setStatus(f func(*Status)) {
	c.mu.Lock()
	f(&c.status)
	c.mu.Unlock()
}

// AuthorizeURL starts the OAuth flow. The state is remembered for 15 minutes.
func (c *Client) AuthorizeURL(ctx context.Context, redirectURI string) (string, error) {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	state := hex.EncodeToString(b) // 24 chars; Whoop needs at least 8
	if err := c.Store.SetSyncState(ctx, c.cfg.Name+":oauth_state",
		state+" "+strconv.FormatInt(time.Now().Unix(), 10)+" "+redirectURI); err != nil {
		return "", err
	}
	q := url.Values{
		"response_type": {"code"},
		"client_id":     {c.Config().ClientID},
		"redirect_uri":  {redirectURI},
		"scope":         {strings.Join(c.Config().Scopes, " ")},
		"state":         {state},
	}
	return c.Config().AuthURL + "?" + q.Encode(), nil
}

// Exchange completes the flow with the authorization code.
func (c *Client) Exchange(ctx context.Context, code, state string) error {
	saved, err := c.Store.SyncState(ctx, c.cfg.Name+":oauth_state")
	if err != nil {
		return errors.New("no sign-in in progress; start again from Settings")
	}
	parts := strings.SplitN(saved, " ", 3)
	if len(parts) != 3 || parts[0] != state {
		return errors.New("sign-in state did not match; start again from Settings")
	}
	if at, _ := strconv.ParseInt(parts[1], 10, 64); time.Since(time.Unix(at, 0)) > 15*time.Minute {
		return errors.New("sign-in took too long; start again from Settings")
	}
	tok, err := c.tokenRequest(ctx, url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {code},
		"redirect_uri": {parts[2]},
	})
	if err != nil {
		return err
	}
	_ = c.Store.SetSyncState(ctx, c.cfg.Name+":oauth_state", "used")
	return c.Store.PutToken(ctx, tok)
}

func (c *Client) tokenRequest(ctx context.Context, form url.Values) (store.Token, error) {
	form.Set("client_id", c.Config().ClientID)
	form.Set("client_secret", c.Config().ClientSecret)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Config().TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return store.Token{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return store.Token{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return store.Token{}, fmt.Errorf("%s token endpoint: %d %s", c.cfg.Name, resp.StatusCode, truncate(string(body), 200))
	}
	var t struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
		Scope        string `json:"scope"`
	}
	if err := json.Unmarshal(body, &t); err != nil || t.AccessToken == "" {
		return store.Token{}, fmt.Errorf("%s token endpoint: unreadable response", c.cfg.Name)
	}
	if t.ExpiresIn == 0 {
		t.ExpiresIn = 3600
	}
	return store.Token{Provider: c.cfg.Name, AccessToken: t.AccessToken, RefreshToken: t.RefreshToken,
		ExpiresAt: time.Now().Add(time.Duration(t.ExpiresIn) * time.Second).UnixMilli(), Scope: t.Scope}, nil
}

// ErrNotConnected means the athlete hasn't signed in to this provider.
var ErrNotConnected = errors.New("not connected")

// token returns a valid access token, refreshing just before use. Refresh
// tokens may be single-use (Wahoo), so the new one is saved immediately.
func (c *Client) token(ctx context.Context) (string, error) {
	t, err := c.Store.GetToken(ctx, c.cfg.Name)
	if errors.Is(err, store.ErrNotFound) {
		return "", ErrNotConnected
	} else if err != nil {
		return "", err
	}
	if time.Now().Add(2*time.Minute).UnixMilli() < t.ExpiresAt {
		return t.AccessToken, nil
	}
	if t.RefreshToken == "" {
		return "", fmt.Errorf("%s session expired; reconnect in Settings", c.cfg.Name)
	}
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {t.RefreshToken}}
	if c.cfg.Name == "whoop" {
		form.Set("scope", "offline")
	}
	nt, err := c.tokenRequest(ctx, form)
	if err != nil {
		return "", err
	}
	if nt.RefreshToken == "" {
		nt.RefreshToken = t.RefreshToken
	}
	if err := c.Store.PutToken(ctx, nt); err != nil {
		return "", err
	}
	return nt.AccessToken, nil
}

// GetJSON performs an authenticated GET and decodes JSON into v.
func (c *Client) GetJSON(ctx context.Context, u string, v any) error {
	tok, err := c.token(ctx)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		return errRateLimited
	}
	if resp.StatusCode == http.StatusUnauthorized {
		// Force a refresh next time.
		if t, err := c.Store.GetToken(ctx, c.cfg.Name); err == nil {
			t.ExpiresAt = 0
			_ = c.Store.PutToken(ctx, t)
		}
		return fmt.Errorf("%s: unauthorized", c.cfg.Name)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("%s %s: %d %s", c.cfg.Name, u, resp.StatusCode, truncate(string(b), 200))
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(v)
}

var errRateLimited = errors.New("rate limited")

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// Loop runs sync every interval until ctx ends, recording status.
func (c *Client) Loop(ctx context.Context, interval time.Duration, sync func(context.Context) error, after func(context.Context)) {
	backoff := interval
	for {
		// Not set up yet: wait for credentials from Settings.
		if !c.Config().Configured() {
			select {
			case <-ctx.Done():
				return
			case <-c.kick():
			}
			continue
		}
		c.setStatus(func(s *Status) { s.Syncing = true })
		err := sync(ctx)
		c.setStatus(func(s *Status) {
			s.Syncing = false
			if err != nil && !errors.Is(err, ErrNotConnected) && ctx.Err() == nil {
				s.LastError = err.Error()
			} else {
				s.LastError = ""
			}
		})
		wait := interval
		switch {
		case err == nil:
			_ = c.Store.SetSyncState(ctx, c.cfg.Name+":last_sync", strconv.FormatInt(time.Now().UnixMilli(), 10))
			backoff = interval
			if after != nil {
				after(ctx)
			}
		case errors.Is(err, ErrNotConnected):
			wait = time.Minute // pick up a new connection quickly
		case ctx.Err() == nil:
			c.Log.Warn("sync failed", "provider", c.cfg.Name, "err", err.Error())
			backoff = min(backoff*2, 6*time.Hour)
			wait = backoff
		}
		select {
		case <-ctx.Done():
			return
		case <-c.kick():
		case <-time.After(wait):
		}
	}
}

var kicks sync.Map // provider name -> chan struct{}

func (c *Client) kick() chan struct{} {
	ch, _ := kicks.LoadOrStore(c.cfg.Name, make(chan struct{}, 1))
	return ch.(chan struct{})
}

// SyncNow wakes the loop (after connecting, or from a "Sync now" button).
func (c *Client) SyncNow() {
	select {
	case c.kick() <- struct{}{}:
	default:
	}
}
