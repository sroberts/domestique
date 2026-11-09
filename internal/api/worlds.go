package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // Zwift's maps are PNGs
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/image/draw"

	"domestique/internal/activity"
)

// WorldMaps serves Zwift's map image of each world, for drawing under routes
// ridden there. The images are Zwift's artwork, so they're never shipped:
// the first request for a world downloads its image from Zwift, scales it to
// fit a phone's GPU, and keeps it in Dir. The browser can't draw Zwift's copy
// itself, because Zwift's CDN doesn't allow other sites to use it.
type WorldMaps struct {
	Dir  string       // under the storage directory, so backups keep it
	HTTP *http.Client // nil: a client with a one-minute timeout

	mu     sync.Mutex // one download at a time: decoding one takes ~150 MB
	failed map[string]time.Time
}

const (
	// maxMapSide is the largest texture many phone GPUs can draw.
	maxMapSide = 4096
	// maxMapBytes and maxMapPixels bound what a download may cost.
	maxMapBytes  = 32 << 20
	maxMapPixels = 80_000_000
	// retryMapAfter is how long a failed download is left before trying
	// Zwift again, so a map that won't load doesn't fetch on every view.
	retryMapAfter = 10 * time.Minute
)

var errMapUnavailable = errors.New("map unavailable")

func (a *API) registerWorlds(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/worlds/{slug}/map", a.worldMap)
}

func (a *API) worldMap(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	src := activity.ZwiftMapURL(slug)
	if src == "" || a.WorldMaps == nil {
		writeErr(w, http.StatusNotFound, "no map for this world")
		return
	}
	// Finish a download even if this viewer leaves: the next one gets it.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Minute)
	defer cancel()
	path, err := a.WorldMaps.get(ctx, slug, src)
	if err != nil {
		if !errors.Is(err, errMapUnavailable) {
			a.Log.Warn("zwift map", "world", slug, "err", err.Error())
		}
		writeErr(w, http.StatusBadGateway, "Zwift's map of this world couldn't be fetched")
		return
	}
	f, err := os.Open(path)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		a.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	// The file name changes with Zwift's URL, so a month is safe.
	w.Header().Set("Cache-Control", "private, max-age=2592000")
	http.ServeContent(w, r, "", st.ModTime(), f)
}

// get returns the cached file for the world, downloading it first if needed.
func (m *WorldMaps) get(ctx context.Context, slug, src string) (string, error) {
	sum := sha256.Sum256([]byte(src))
	name := slug + "-" + hex.EncodeToString(sum[:4]) + ".jpg"
	path := filepath.Join(m.Dir, name)
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := os.Stat(path); err == nil { // another request fetched it
		return path, nil
	}
	if t, ok := m.failed[slug]; ok && time.Since(t) < retryMapAfter {
		return "", errMapUnavailable
	}
	if err := m.fetch(ctx, src, path); err != nil {
		if m.failed == nil {
			m.failed = map[string]time.Time{}
		}
		m.failed[slug] = time.Now()
		return "", err
	}
	delete(m.failed, slug)
	// A copy from Zwift's old URL for this world is no longer needed.
	old, _ := filepath.Glob(filepath.Join(m.Dir, slug+"-*.jpg"))
	for _, p := range old {
		if p != path {
			_ = os.Remove(p)
		}
	}
	return path, nil
}

func (m *WorldMaps) fetch(ctx context.Context, src, path string) error {
	client := m.HTTP
	if client == nil {
		client = &http.Client{Timeout: time.Minute}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return err
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("zwift answered %s", res.Status)
	}
	if ct := res.Header.Get("Content-Type"); ct != "" && !strings.HasPrefix(ct, "image/") {
		return fmt.Errorf("zwift sent %s, not an image", ct)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, maxMapBytes+1))
	if err != nil {
		return err
	}
	if len(body) > maxMapBytes {
		return errors.New("image too large")
	}
	out, err := scaleMap(body)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(m.Dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(m.Dir, ".part-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op once renamed
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// scaleMap decodes an image, shrinks it so neither side is over maxMapSide,
// and encodes it as a JPEG, about a quarter the size of Zwift's PNG.
func scaleMap(body []byte) ([]byte, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("not an image: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > maxMapPixels {
		return nil, fmt.Errorf("image is %dx%d", cfg.Width, cfg.Height)
	}
	img, _, err := image.Decode(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	if s := float64(maxMapSide) / float64(max(b.Dx(), b.Dy())); s < 1 {
		dst := image.NewRGBA(image.Rect(0, 0, max(1, int(float64(b.Dx())*s)), max(1, int(float64(b.Dy())*s))))
		draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
		img = dst
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: 85}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
