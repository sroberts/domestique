// Package webui serves the PWA, which Vite builds into dist/ and the binary embeds.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// Handler serves static files, falling back to index.html for app routes so
// deep links like /session/<id> work on reload.
func Handler() http.Handler {
	root, _ := fs.Sub(dist, "dist")
	files := http.FileServer(http.FS(root))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p == "" {
			p = "index.html"
		}
		if st, err := fs.Stat(root, p); err != nil || st.IsDir() {
			if path.Ext(p) != "" {
				http.NotFound(w, r) // a missing asset, not an app route
				return
			}
			p = "index.html"
		}
		if _, err := fs.Stat(root, "index.html"); err != nil {
			http.Error(w, "web UI not built: run `make web`", http.StatusServiceUnavailable)
			return
		}

		switch {
		case strings.HasPrefix(p, "assets/"):
			// Vite content-hashes everything under assets/.
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		default:
			// index.html, sw.js, the manifest and icons must revalidate so an
			// update reaches the phone.
			w.Header().Set("Cache-Control", "no-cache")
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		// Sibling apps behind the gateway are same-site: none may frame us
		// and trick a tap on Disconnect or a set's weight.
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "frame-ancestors 'none'")
		if p == "manifest.webmanifest" {
			w.Header().Set("Content-Type", "application/manifest+json")
		}
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/" + p
		if p == "index.html" {
			// FileServer redirects /index.html to /; serve the bytes directly.
			b, err := fs.ReadFile(root, "index.html")
			if err != nil {
				http.Error(w, "web UI missing", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(b)
			return
		}
		files.ServeHTTP(w, r2)
	})
}
