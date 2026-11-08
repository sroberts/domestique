// Package auth implements the Outpost login-gateway contract (outpost-service-spec §3):
// no login of our own, identity from the gateway's headers, the signed identity
// token verified for anything that changes state, and Origin checks on every
// state-changing request.
package auth

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

type ctxKey struct{}

// User is the caller as the gateway reports it.
type User struct {
	Username string `json:"username"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Verified bool   `json:"verified"` // true when taken from a verified X-Outpost-Identity token
}

// UserFrom returns the request's user, if the gateway supplied one.
func UserFrom(ctx context.Context) (User, bool) {
	u, ok := ctx.Value(ctxKey{}).(User)
	return u, ok
}

// Gateway holds the gateway configuration read from Outpost's environment.
type Gateway struct {
	Enabled bool
	issuer  string
	jwksURL string
	log     *slog.Logger
	client  *http.Client
	// hosts are the names we are served at, from DOMESTIQUE_PUBLIC_URL and
	// DOMESTIQUE_ALLOWED_HOSTS (lower case, no port). Behind the gateway they
	// pin the identity token's audience; without it they extend the Host
	// allowlist that stops DNS rebinding.
	hosts []string

	mu        sync.Mutex
	keys      map[string]ed25519.PublicKey
	tried     map[string]time.Time // last refetch attempted for an unknown kid
	lastFetch time.Time            // last JWKS fetch of any kind
	minFetch  time.Duration        // least time between JWKS fetches
}

// FromEnv reads OUTPOST_AUTH, OUTPOST_ISSUER, OUTPOST_JWKS_URL,
// DOMESTIQUE_PUBLIC_URL and DOMESTIQUE_ALLOWED_HOSTS.
func FromEnv(log *slog.Logger) *Gateway {
	g := &Gateway{
		Enabled:  os.Getenv("OUTPOST_AUTH") == "gateway",
		issuer:   os.Getenv("OUTPOST_ISSUER"),
		jwksURL:  os.Getenv("OUTPOST_JWKS_URL"),
		log:      log,
		client:   &http.Client{Timeout: 5 * time.Second},
		keys:     map[string]ed25519.PublicKey{},
		tried:    map[string]time.Time{},
		minFetch: 5 * time.Second,
	}
	if u, err := url.Parse(os.Getenv("DOMESTIQUE_PUBLIC_URL")); err == nil && u.Host != "" {
		g.hosts = append(g.hosts, hostOnly(u.Host))
	}
	for _, h := range strings.Split(os.Getenv("DOMESTIQUE_ALLOWED_HOSTS"), ",") {
		if h = strings.TrimSpace(h); h != "" {
			g.hosts = append(g.hosts, hostOnly(h))
		}
	}
	if g.Enabled && len(g.hosts) == 0 {
		// Without a pinned host the token's audience can only be checked
		// against the request's own Host, which a process on this machine
		// connecting to the loopback port directly chooses itself.
		log.Warn("DOMESTIQUE_PUBLIC_URL is not set: identity-token audience is checked against the request's Host; " +
			"set it (or DOMESTIQUE_ALLOWED_HOSTS) so a sibling service can't replay its tokens here")
	}
	return g
}

// hostOnly lower-cases a host[:port] and drops the port.
func hostOnly(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	if host, _, err := net.SplitHostPort(h); err == nil {
		return strings.Trim(host, "[]")
	}
	return strings.Trim(h, "[]")
}

func (g *Gateway) pinned(host string) bool {
	host = hostOnly(host)
	for _, h := range g.hosts {
		if h == host {
			return true
		}
	}
	return false
}

// hostAllowed is the DNS-rebinding guard used when no gateway is in front: a
// page on another site whose name has been re-pointed at 127.0.0.1 is same
// origin with us as far as the browser is concerned, so the Host it sends is
// the only tell. Names an attacker cannot point at this machine are allowed:
// IP literals, localhost, single-label and .local names, Tailscale's
// *.ts.net, and the configured hosts.
func (g *Gateway) hostAllowed(host string) bool {
	h := hostOnly(host)
	switch {
	case h == "":
		return true // HTTP/1.0 or a hand-rolled client: not a browser
	case net.ParseIP(h) != nil, h == "localhost", strings.HasSuffix(h, ".localhost"),
		!strings.Contains(h, "."), strings.HasSuffix(h, ".local"), strings.HasSuffix(h, ".ts.net"):
		return true
	}
	return g.pinned(h)
}

// Middleware enforces Origin checks and, behind the gateway, identity.
// The healthcheck must be mounted outside it.
func (g *Gateway) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		unsafe := r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions

		if !g.Enabled && !g.hostAllowed(r.Host) {
			g.log.Warn("rejected unknown host", "host", r.Host, "path", r.URL.Path)
			writeErr(w, http.StatusMisdirectedRequest, "unknown host: set DOMESTIQUE_ALLOWED_HOSTS")
			return
		}
		if unsafe {
			if err := checkOrigin(r); err != nil {
				g.log.Warn("rejected cross-origin write", "path", r.URL.Path, "err", err.Error())
				writeErr(w, http.StatusForbidden, "cross-origin request refused")
				return
			}
		}

		ctx := r.Context()
		if g.Enabled {
			// Headers are only trustworthy when the gateway is in front of us,
			// and even then another process on this machine can reach our
			// loopback port directly. Training and health data is personal, so
			// every API and MCP request must carry a valid signed identity, not
			// just writes. Static app files need nothing: they hold no data.
			if (dataPath(r.URL.Path) || unsafe) && !publicPath(r) {
				u, err := g.verify(r)
				if err != nil {
					g.log.Warn("identity token rejected", "path", r.URL.Path, "err", err.Error())
					writeErr(w, http.StatusUnauthorized, "sign in again")
					return
				}
				u.Name = r.Header.Get("X-Outpost-Name")
				u.Email = r.Header.Get("X-Outpost-Email")
				ctx = context.WithValue(ctx, ctxKey{}, u)
			}
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// WhoopWebhookPath is served without a gateway identity: Whoop's servers
// can't sign in. It must be listed in outpost.toml public_paths, and its
// handler verifies Whoop's HMAC signature instead.
const WhoopWebhookPath = "/v1/webhooks/whoop"

// WahooWebhookPath is the same for Wahoo, whose handler checks its token.
const WahooWebhookPath = "/v1/webhooks/wahoo"

// publicPath reports whether a request may skip the identity check. Only the
// exact path and method count; anything else under /v1/ still needs one.
func publicPath(r *http.Request) bool {
	return r.Method == http.MethodPost && (r.URL.Path == WhoopWebhookPath || r.URL.Path == WahooWebhookPath)
}

func dataPath(p string) bool {
	return strings.HasPrefix(p, "/v1/") || p == "/mcp" || strings.HasPrefix(p, "/mcp/")
}

// requestHost is the host the browser addressed: the gateway preserves Host
// and also sets X-Forwarded-Host.
func requestHost(r *http.Request) string {
	if h := r.Header.Get("X-Forwarded-Host"); h != "" {
		return strings.TrimSpace(strings.Split(h, ",")[0])
	}
	return r.Host
}

// checkOrigin refuses state-changing requests made by another site's pages,
// including sibling apps behind the same gateway (same-site, not same-origin).
func checkOrigin(r *http.Request) error {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "cross-site", "same-site":
		return fmt.Errorf("sec-fetch-site %s", r.Header.Get("Sec-Fetch-Site"))
	}
	src := r.Header.Get("Origin")
	if src == "" {
		src = r.Header.Get("Referer")
	}
	if src == "" {
		// Non-browser client (API token, curl). Browsers always send Origin on writes.
		return nil
	}
	u, err := url.Parse(src)
	if err != nil || u.Host == "" {
		return fmt.Errorf("unparseable origin %q", src)
	}
	if !strings.EqualFold(u.Host, requestHost(r)) {
		return fmt.Errorf("origin %s does not match host %s", u.Host, requestHost(r))
	}
	return nil
}

// ---- Identity token verification -------------------------------------------------

type jwtHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
}

type claims struct {
	Iss string          `json:"iss"`
	Sub string          `json:"sub"`
	Aud json.RawMessage `json:"aud"`
	Exp int64           `json:"exp"`
	Nbf int64           `json:"nbf"`
}

func (c claims) hasAudience(host string) bool {
	host = hostOnly(host)
	if host == "" {
		return false
	}
	var one string
	if json.Unmarshal(c.Aud, &one) == nil {
		return hostOnly(one) == host
	}
	var many []string
	if json.Unmarshal(c.Aud, &many) == nil {
		for _, a := range many {
			if hostOnly(a) == host {
				return true
			}
		}
	}
	return false
}

// audienceOK checks aud against the pinned hosts when configured. Only
// without them does it fall back to the request's Host, which a direct
// loopback caller controls.
func (g *Gateway) audienceOK(c claims, r *http.Request) bool {
	if len(g.hosts) == 0 {
		return c.hasAudience(requestHost(r))
	}
	for _, h := range g.hosts {
		if c.hasAudience(h) {
			return true
		}
	}
	return false
}

func (g *Gateway) verify(r *http.Request) (User, error) {
	tok := r.Header.Get("X-Outpost-Identity")
	if tok == "" {
		return User{}, errors.New("no identity token")
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return User{}, errors.New("malformed token")
	}
	var h jwtHeader
	if err := decodeSegment(parts[0], &h); err != nil {
		return User{}, fmt.Errorf("header: %w", err)
	}
	if h.Alg != "EdDSA" {
		return User{}, fmt.Errorf("unexpected alg %q", h.Alg)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return User{}, fmt.Errorf("signature: %w", err)
	}
	key, err := g.key(r.Context(), h.Kid)
	if err != nil {
		return User{}, err
	}
	if !ed25519.Verify(key, []byte(parts[0]+"."+parts[1]), sig) {
		return User{}, errors.New("bad signature")
	}
	var c claims
	if err := decodeSegment(parts[1], &c); err != nil {
		return User{}, fmt.Errorf("claims: %w", err)
	}
	now := time.Now().Unix()
	const skew = 30
	switch {
	case c.Exp == 0 || now > c.Exp+skew:
		return User{}, errors.New("token expired")
	case c.Nbf != 0 && now+skew < c.Nbf:
		return User{}, errors.New("token not yet valid")
	case g.issuer != "" && c.Iss != g.issuer:
		return User{}, fmt.Errorf("issuer %q", c.Iss)
	case !g.audienceOK(c, r):
		return User{}, fmt.Errorf("audience does not match %s", requestHost(r))
	case c.Sub == "":
		return User{}, errors.New("no subject")
	}
	return User{Username: c.Sub, Verified: true}, nil
}

func decodeSegment(seg string, v any) error {
	b, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// key returns the public key for kid. An unknown kid (Outpost rotated its
// key) triggers an immediate refetch, but each kid at most every 30 seconds,
// so a stream of junk kids can't turn into a stream of JWKS requests.
func (g *Gateway) key(ctx context.Context, kid string) (ed25519.PublicKey, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if k, ok := g.lookup(kid); ok {
		return k, nil
	}
	if t, ok := g.tried[kid]; ok && time.Since(t) < 30*time.Second {
		return nil, fmt.Errorf("unknown key %q", kid)
	}
	// However many distinct kids arrive, fetch at most once every few
	// seconds: unauthenticated callers choose kids freely. Any fetch loads a
	// rotated key, so a genuine new kid waits at most this long.
	if !g.lastFetch.IsZero() && time.Since(g.lastFetch) < g.minFetch {
		return nil, fmt.Errorf("unknown key %q", kid)
	}
	g.lastFetch = time.Now()
	if len(g.tried) > 64 {
		g.tried = map[string]time.Time{}
	}
	g.tried[kid] = time.Now()
	if err := g.fetch(ctx); err != nil {
		return nil, fmt.Errorf("fetch jwks: %w", err)
	}
	if k, ok := g.lookup(kid); ok {
		return k, nil
	}
	return nil, fmt.Errorf("unknown key %q", kid)
}

func (g *Gateway) lookup(kid string) (ed25519.PublicKey, bool) {
	if kid == "" && len(g.keys) == 1 {
		for _, k := range g.keys {
			return k, true
		}
	}
	k, ok := g.keys[kid]
	return k, ok
}

func (g *Gateway) fetch(ctx context.Context) error {
	if g.jwksURL == "" {
		return errors.New("OUTPOST_JWKS_URL is not set")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.jwksURL, nil)
	if err != nil {
		return err
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	var set struct {
		Keys []struct {
			Kty string `json:"kty"`
			Crv string `json:"crv"`
			X   string `json:"x"`
			Kid string `json:"kid"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&set); err != nil {
		return err
	}
	keys := map[string]ed25519.PublicKey{}
	for _, k := range set.Keys {
		if k.Kty != "OKP" || k.Crv != "Ed25519" {
			continue
		}
		raw, err := base64.RawURLEncoding.DecodeString(k.X)
		if err != nil || len(raw) != ed25519.PublicKeySize {
			continue
		}
		keys[k.Kid] = ed25519.PublicKey(raw)
	}
	if len(keys) == 0 {
		return errors.New("no Ed25519 keys")
	}
	g.keys = keys
	return nil
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
