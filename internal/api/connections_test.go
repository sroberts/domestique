package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Starting OAuth replaces the pending state, so another site must not be
// able to trigger it with a link or an <img>.
func TestOAuthStartRefusesCrossSite(t *testing.T) {
	a := &API{}
	mux := http.NewServeMux()
	a.registerConnections(mux)
	for _, site := range []string{"cross-site", "same-site"} {
		r := httptest.NewRequest("GET", "/v1/oauth/whoop/start", nil)
		r.Header.Set("Sec-Fetch-Site", site)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s: %d, want 403", site, w.Code)
		}
	}
	// Our own page gets as far as the provider lookup.
	r := httptest.NewRequest("GET", "/v1/oauth/whoop/start", nil)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("same-origin: %d, want 404 (no provider configured)", w.Code)
	}
}
