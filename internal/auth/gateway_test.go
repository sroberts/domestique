package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeOutpost stands in for the login gateway's JWKS and token signing.
type fakeOutpost struct {
	priv ed25519.PrivateKey
	kid  string
	jwks *httptest.Server
}

func newFakeOutpost(t *testing.T) *fakeOutpost {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	f := &fakeOutpost{priv: priv, kid: "k1"}
	f.jwks = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := f.priv.Public().(ed25519.PublicKey)
		_ = pub
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "OKP", "crv": "Ed25519", "kid": f.kid, "x": base64.RawURLEncoding.EncodeToString(cur),
		}}})
	}))
	t.Cleanup(f.jwks.Close)
	return f
}

func (f *fakeOutpost) token(sub, iss, aud string, exp time.Time, key ed25519.PrivateKey) string {
	h, _ := json.Marshal(map[string]string{"alg": "EdDSA", "typ": "JWT", "kid": f.kid})
	c, _ := json.Marshal(map[string]any{"sub": sub, "iss": iss, "aud": aud, "exp": exp.Unix(), "name": "Scott"})
	signing := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(c)
	return signing + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, []byte(signing)))
}

const (
	host   = "domestique.tail1234.ts.net"
	issuer = "https://outpost.tail1234.ts.net"
)

func gatewayFor(t *testing.T, f *fakeOutpost, enabled bool) http.Handler {
	t.Helper()
	return gatewayWith(t, f, enabled, nil)
}

func gatewayWith(t *testing.T, f *fakeOutpost, enabled bool, tweak func(*Gateway)) http.Handler {
	t.Helper()
	if enabled {
		t.Setenv("OUTPOST_AUTH", "gateway")
	} else {
		t.Setenv("OUTPOST_AUTH", "")
	}
	t.Setenv("OUTPOST_ISSUER", issuer)
	t.Setenv("OUTPOST_JWKS_URL", f.jwks.URL)
	g := FromEnv(slog.New(slog.NewTextHandler(io.Discard, nil)))
	g.minFetch = 0
	if tweak != nil {
		tweak(g)
	}
	return g.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFrom(r.Context())
		if ok {
			w.Header().Set("X-Test-User", u.Username)
		}
		w.WriteHeader(http.StatusOK)
	}))
}

func do(h http.Handler, method, path string, hdr map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader("{}"))
	r.Host = host
	r.Header.Set("X-Forwarded-Host", host)
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestGatewayContract(t *testing.T) {
	f := newFakeOutpost(t)
	h := gatewayFor(t, f, true)
	good := f.token("scott", issuer, host, time.Now().Add(60*time.Second), f.priv)
	_, otherKey, _ := ed25519.GenerateKey(rand.Reader)

	cases := []struct {
		name   string
		method string
		path   string
		hdr    map[string]string
		code   int
		user   string
	}{
		{"read without identity: a neighbour on loopback", "GET", "/v1/today", nil, 401, ""},
		{"spoofed plain header is not enough", "GET", "/v1/today", map[string]string{"X-Outpost-User": "scott"}, 401, ""},
		{"read with identity", "GET", "/v1/today", map[string]string{"X-Outpost-Identity": good}, 200, "scott"},
		{"mcp with identity, no Origin (portal client)", "POST", "/mcp", map[string]string{"X-Outpost-Identity": good}, 200, "scott"},
		{"static app file needs nothing", "GET", "/assets/index.js", nil, 200, ""},
		{"healthcheck path is not behind identity", "GET", "/up", nil, 200, ""},
		{"expired token", "GET", "/v1/today", map[string]string{"X-Outpost-Identity": f.token("scott", issuer, host, time.Now().Add(-2*time.Minute), f.priv)}, 401, ""},
		{"wrong audience (another app's token)", "GET", "/v1/today", map[string]string{"X-Outpost-Identity": f.token("scott", issuer, "yarr.tail1234.ts.net", time.Now().Add(time.Minute), f.priv)}, 401, ""},
		{"wrong issuer", "GET", "/v1/today", map[string]string{"X-Outpost-Identity": f.token("scott", "https://evil", host, time.Now().Add(time.Minute), f.priv)}, 401, ""},
		{"forged signature", "GET", "/v1/today", map[string]string{"X-Outpost-Identity": f.token("scott", issuer, host, time.Now().Add(time.Minute), otherKey)}, 401, ""},
		{"write from a sibling app's page", "PUT", "/v1/sets/x", map[string]string{"X-Outpost-Identity": good, "Origin": "https://yarr.tail1234.ts.net", "Sec-Fetch-Site": "same-site"}, 403, ""},
		{"write from our own page", "PUT", "/v1/sets/x", map[string]string{"X-Outpost-Identity": good, "Origin": "https://" + host, "Sec-Fetch-Site": "same-origin"}, 200, "scott"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := do(h, c.method, c.path, c.hdr)
			if w.Code != c.code || w.Header().Get("X-Test-User") != c.user {
				t.Fatalf("got %d user %q, want %d user %q", w.Code, w.Header().Get("X-Test-User"), c.code, c.user)
			}
		})
	}
}

func TestGatewayKeyRotation(t *testing.T) {
	f := newFakeOutpost(t)
	h := gatewayFor(t, f, true)
	if w := do(h, "GET", "/v1/today", map[string]string{"X-Outpost-Identity": f.token("scott", issuer, host, time.Now().Add(time.Minute), f.priv)}); w.Code != 200 {
		t.Fatalf("before rotation: %d", w.Code)
	}
	// Outpost rotates its key; the first token under the new kid triggers a refetch.
	_, f.priv, _ = ed25519.GenerateKey(rand.Reader)
	f.kid = "k2"
	if w := do(h, "GET", "/v1/today", map[string]string{"X-Outpost-Identity": f.token("scott", issuer, host, time.Now().Add(time.Minute), f.priv)}); w.Code != 200 {
		t.Fatalf("after rotation: %d", w.Code)
	}
}

func TestNoGatewayTrustsNoHeaders(t *testing.T) {
	f := newFakeOutpost(t)
	h := gatewayFor(t, f, false)
	w := do(h, "GET", "/v1/today", map[string]string{"X-Outpost-User": "mallory"})
	if w.Code != 200 || w.Header().Get("X-Test-User") != "" {
		t.Fatalf("without the gateway, X-Outpost-User must be ignored: %d %q", w.Code, w.Header().Get("X-Test-User"))
	}
	// Origin checks still apply to writes.
	if w := do(h, "POST", "/v1/hr", map[string]string{"Origin": "https://evil.example"}); w.Code != 403 {
		t.Fatalf("cross-origin write without gateway: %d", w.Code)
	}
}

// A sibling service receives the user's tokens with its own audience. Without
// a pinned host it can replay one here by naming itself in Host and
// X-Forwarded-Host on a direct loopback connection; with one it can't.
func TestGatewayAudiencePinned(t *testing.T) {
	f := newFakeOutpost(t)
	sibling := "yarr.tail1234.ts.net"
	t.Setenv("DOMESTIQUE_PUBLIC_URL", "https://"+host+"/")
	h := gatewayFor(t, f, true)
	r := httptest.NewRequest("GET", "/v1/today", nil)
	r.Host = sibling
	r.Header.Set("X-Forwarded-Host", sibling)
	r.Header.Set("X-Outpost-Identity", f.token("scott", issuer, sibling, time.Now().Add(time.Minute), f.priv))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("replayed sibling token with spoofed Host: %d, want 401", w.Code)
	}
	good := f.token("scott", issuer, host, time.Now().Add(time.Minute), f.priv)
	if w := do(h, "GET", "/v1/today", map[string]string{"X-Outpost-Identity": good}); w.Code != 200 {
		t.Fatalf("our own token: %d", w.Code)
	}

	// A renamed node is listed in DOMESTIQUE_ALLOWED_HOSTS.
	t.Setenv("DOMESTIQUE_ALLOWED_HOSTS", "domestique-1.tail1234.ts.net")
	h = gatewayFor(t, f, true)
	renamed := f.token("scott", issuer, "domestique-1.tail1234.ts.net", time.Now().Add(time.Minute), f.priv)
	if w := do(h, "GET", "/v1/today", map[string]string{"X-Outpost-Identity": renamed}); w.Code != 200 {
		t.Fatalf("allowed host: %d", w.Code)
	}
}

// Junk kids from unauthenticated callers must not each cost a JWKS fetch.
func TestGatewayJunkKidsRateLimited(t *testing.T) {
	f := newFakeOutpost(t)
	var fetches int
	inner := f.jwks.Config.Handler
	f.jwks.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetches++
		inner.ServeHTTP(w, r)
	})
	h := gatewayWith(t, f, true, func(g *Gateway) { g.minFetch = time.Hour })
	for i := 0; i < 50; i++ {
		f.kid = "junk" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		do(h, "GET", "/v1/today", map[string]string{"X-Outpost-Identity": f.token("scott", issuer, host, time.Now().Add(time.Minute), f.priv)})
	}
	if fetches > 1 {
		t.Fatalf("%d JWKS fetches for 50 junk kids, want 1", fetches)
	}
}

// Without the gateway nothing authenticates requests, so a site whose DNS
// name is re-pointed at 127.0.0.1 must not be served.
func TestNoGatewayRefusesRebindHost(t *testing.T) {
	f := newFakeOutpost(t)
	h := gatewayFor(t, f, false)
	for _, c := range []struct {
		host string
		code int
	}{
		{"evil.example:8095", 421},
		{"127.0.0.1:8095", 200},
		{"localhost:8095", 200},
		{"[::1]:8095", 200},
		{"macmini", 200},
		{"macmini.local", 200},
		{host, 200},
	} {
		r := httptest.NewRequest("GET", "/v1/today", nil)
		r.Host = c.host
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != c.code {
			t.Errorf("Host %s: %d, want %d", c.host, w.Code, c.code)
		}
	}
	t.Setenv("DOMESTIQUE_ALLOWED_HOSTS", "fitness.example.com")
	h = gatewayFor(t, f, false)
	r := httptest.NewRequest("PUT", "/v1/athlete", nil)
	r.Host = "fitness.example.com"
	r.Header.Set("Origin", "https://fitness.example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("configured host: %d", w.Code)
	}
}
