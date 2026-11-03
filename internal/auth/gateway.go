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
	"log/slog"
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

	mu    sync.Mutex
	keys  map[string]ed25519.PublicKey
	tried map[string]time.Time // last refetch attempted for an unknown kid
}

// FromEnv reads OUTPOST_AUTH, OUTPOST_ISSUER and OUTPOST_JWKS_URL.
func FromEnv(log *slog.Logger) *Gateway {
	return &Gateway{
		Enabled: os.Getenv("OUTPOST_AUTH") == "gateway",
		issuer:  os.Getenv("OUTPOST_ISSUER"),
		jwksURL: os.Getenv("OUTPOST_JWKS_URL"),
		log:     log,
		client:  &http.Client{Timeout: 5 * time.Second},
		keys:    map[string]ed25519.PublicKey{},
		tried:   map[string]time.Time{},
	}
}

// Middleware enforces Origin checks and, behind the gateway, identity.
// The healthcheck must be mounted outside it.
func (g *Gateway) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		unsafe := !(r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions)

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
			if dataPath(r.URL.Path) || unsafe {
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
	var one string
	if json.Unmarshal(c.Aud, &one) == nil {
		return strings.EqualFold(one, host)
	}
	var many []string
	if json.Unmarshal(c.Aud, &many) == nil {
		for _, a := range many {
			if strings.EqualFold(a, host) {
				return true
			}
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
	case !c.hasAudience(requestHost(r)):
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
	if err := json.NewDecoder(resp.Body).Decode(&set); err != nil {
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
