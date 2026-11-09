package api

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// fakeZwift answers every request, whatever its host, from h, and counts them.
func fakeZwift(t *testing.T, h http.HandlerFunc) (*http.Client, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return &http.Client{Transport: rewrite{u}}, &n
}

type rewrite struct{ to *url.URL }

func (rw rewrite) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.URL.Scheme, r.URL.Host = rw.to.Scheme, rw.to.Host
	return http.DefaultTransport.RoundTrip(r)
}

func pngOf(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewPaletted(image.Rect(0, 0, w, h), color.Palette{color.RGBA{8, 134, 228, 255}, color.RGBA{124, 153, 56, 255}})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func worldsAPI(t *testing.T, h http.HandlerFunc) (http.Handler, *atomic.Int32, string) {
	t.Helper()
	a, mux := newTestAPI(t)
	client, n := fakeZwift(t, h)
	dir := filepath.Join(t.TempDir(), "cache", "worlds")
	a.WorldMaps = &WorldMaps{Dir: dir, HTTP: client}
	return mux, n, dir
}

func TestWorldMapDownloadsScalesAndCaches(t *testing.T) {
	src := pngOf(t, 6000, 3000)
	mux, n, dir := worldsAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/static/images/maps/MiniMap_Watopia_2.png" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(src)
	})
	// A copy from an older URL for the same world is cleared out.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, "watopia-00000000.jpg")
	if err := os.WriteFile(stale, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	for i := range 2 {
		w := do(t, mux, http.MethodGet, "/v1/worlds/watopia/map", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("request %d: %d %s", i, w.Code, w.Body)
		}
		if ct := w.Header().Get("Content-Type"); ct != "image/jpeg" {
			t.Fatalf("Content-Type %q", ct)
		}
		cfg, err := jpeg.DecodeConfig(bytes.NewReader(w.Body.Bytes()))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Width != 4096 || cfg.Height != 2048 {
			t.Fatalf("scaled to %dx%d, want 4096x2048", cfg.Width, cfg.Height)
		}
	}
	if n.Load() != 1 {
		t.Fatalf("fetched from Zwift %d times, want once", n.Load())
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale copy still there: %v", err)
	}
	files, _ := os.ReadDir(dir)
	if len(files) != 1 {
		t.Fatalf("cache holds %d files, want 1", len(files))
	}
}

func TestWorldMapSmallImageKeepsItsSize(t *testing.T) {
	mux, _, _ := worldsAPI(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(pngOf(t, 300, 200))
	})
	w := do(t, mux, http.MethodGet, "/v1/worlds/scotland/map", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(w.Body.Bytes()))
	if err != nil || cfg.Width != 300 || cfg.Height != 200 {
		t.Fatalf("got %dx%d, %v", cfg.Width, cfg.Height, err)
	}
}

func TestWorldMapUnknownWorld(t *testing.T) {
	mux, n, _ := worldsAPI(t, func(w http.ResponseWriter, r *http.Request) {})
	for _, p := range []string{"/v1/worlds/atlantis/map", "/v1/worlds/..%2Fwatopia/map", "/v1/worlds/france/map", "/v1/worlds/london/map"} {
		if w := do(t, mux, http.MethodGet, p, nil); w.Code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", p, w.Code)
		}
	}
	if n.Load() != 0 {
		t.Fatalf("fetched %d times for unknown worlds", n.Load())
	}
}

func TestWorldMapOff(t *testing.T) {
	_, mux := newTestAPI(t) // no WorldMaps
	if w := do(t, mux, http.MethodGet, "/v1/worlds/watopia/map", nil); w.Code != http.StatusNotFound {
		t.Fatalf("%d, want 404", w.Code)
	}
}

func TestWorldMapFailuresBackOff(t *testing.T) {
	for _, c := range []struct {
		name string
		h    http.HandlerFunc
	}{
		{"missing", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }},
		{"not an image", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("<html>moved</html>"))
		}},
		{"a web page", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write(pngOf(t, 10, 10))
		}},
		{"too many pixels", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(pngOf(t, 10000, 9000)) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			mux, n, dir := worldsAPI(t, c.h)
			for range 3 {
				if w := do(t, mux, http.MethodGet, "/v1/worlds/makuri-islands/map", nil); w.Code != http.StatusBadGateway {
					t.Fatalf("%d, want 502", w.Code)
				}
			}
			if n.Load() != 1 {
				t.Fatalf("asked Zwift %d times, want once until the retry wait passes", n.Load())
			}
			files, _ := os.ReadDir(dir)
			if len(files) != 0 {
				t.Fatalf("left %d files behind", len(files))
			}
		})
	}
}
