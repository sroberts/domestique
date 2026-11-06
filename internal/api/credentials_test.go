package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"domestique/internal/ingest"
	"domestique/internal/provider"
	"domestique/internal/store"
)

func TestCredentialsFromSettings(t *testing.T) {
	a, h := newTestAPI(t)
	dir := t.TempDir()
	a.Ingest = &ingest.Ingester{Store: a.Store, StorageDir: dir}
	whoop := provider.NewClient(provider.WhoopConfig, provider.FromNone, a.Store, a.Log)
	a.Providers = map[string]*provider.Client{"whoop": whoop}
	const secret = "sup3r-s3cret-value"

	// Not set up: connecting is refused.
	if w := do(t, h, "POST", "/v1/connections/whoop/sync", nil); w.Code != http.StatusConflict {
		t.Fatalf("sync before setup = %d", w.Code)
	}
	// Bad input is refused without echoing it.
	w := do(t, h, "PUT", "/v1/connections/whoop/credentials", map[string]string{"client_id": "abc", "client_secret": "has space"})
	if w.Code != http.StatusBadRequest || strings.Contains(w.Body.String(), "has space") {
		t.Fatalf("bad secret = %d %s", w.Code, w.Body)
	}

	w = do(t, h, "PUT", "/v1/connections/whoop/credentials", map[string]string{"client_id": " abc-123 ", "client_secret": secret + "\n"})
	if w.Code != http.StatusOK {
		t.Fatalf("save = %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), secret) {
		t.Fatal("response contains the secret")
	}
	var st provider.Status
	_ = json.Unmarshal(w.Body.Bytes(), &st)
	if !st.Configured || st.CredentialsFrom != "settings" || st.ClientID != "abc-123" {
		t.Fatalf("status = %+v", st)
	}
	if cfg := whoop.Config(); cfg.ClientSecret != secret {
		t.Fatal("secret not applied live")
	}
	// Files are owner-only, in an owner-only directory, and load back.
	sd := provider.SecretsDir(dir)
	for _, p := range []string{sd, filepath.Join(sd, "whoop-client-secret"), filepath.Join(sd, "whoop-client-id")} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm()&0o077 != 0 {
			t.Fatalf("%s mode %o", p, fi.Mode().Perm())
		}
	}
	if id, sec, err := provider.LoadSaved(sd, "whoop"); err != nil || id != "abc-123" || sec != secret {
		t.Fatalf("LoadSaved = %q %q %v", id, sec, err)
	}
	// The connections list never carries it either.
	if w := do(t, h, "GET", "/v1/connections", nil); strings.Contains(w.Body.String(), secret) {
		t.Fatal("connections list contains the secret")
	}

	// A new client ID drops the old app's tokens; the same ID keeps them.
	ctx := context.Background()
	_ = a.Store.PutToken(ctx, store.Token{Provider: "whoop", AccessToken: "a", RefreshToken: "r", ExpiresAt: 1})
	do(t, h, "PUT", "/v1/connections/whoop/credentials", map[string]string{"client_id": "abc-123", "client_secret": "rotated"})
	if _, err := a.Store.GetToken(ctx, "whoop"); err != nil {
		t.Fatal("same client ID dropped the connection")
	}
	do(t, h, "PUT", "/v1/connections/whoop/credentials", map[string]string{"client_id": "other", "client_secret": "rotated"})
	if _, err := a.Store.GetToken(ctx, "whoop"); err == nil {
		t.Fatal("new client ID kept the old app's tokens")
	}

	// Remove: files, live config and connection all go.
	_ = a.Store.PutToken(ctx, store.Token{Provider: "whoop", AccessToken: "a", RefreshToken: "r", ExpiresAt: 1})
	if w := do(t, h, "DELETE", "/v1/connections/whoop/credentials", nil); w.Code != http.StatusOK {
		t.Fatalf("delete = %d", w.Code)
	}
	if whoop.Config().Configured() {
		t.Fatal("still configured after delete")
	}
	if _, err := os.Stat(filepath.Join(sd, "whoop-client-secret")); !os.IsNotExist(err) {
		t.Fatal("secret file left behind")
	}
	if _, err := a.Store.GetToken(ctx, "whoop"); err == nil {
		t.Fatal("connection kept after delete")
	}
}

func TestCredentialsFromManifestAreLocked(t *testing.T) {
	a, h := newTestAPI(t)
	a.Ingest = &ingest.Ingester{Store: a.Store, StorageDir: t.TempDir()}
	cfg := provider.WhoopConfig
	cfg.ClientID, cfg.ClientSecret = "from-env", "env-secret"
	a.Providers = map[string]*provider.Client{"whoop": provider.NewClient(cfg, provider.FromManifest, a.Store, a.Log)}
	for _, m := range []string{"PUT", "DELETE"} {
		w := do(t, h, m, "/v1/connections/whoop/credentials", map[string]string{"client_id": "x", "client_secret": "y"})
		if w.Code != http.StatusConflict {
			t.Fatalf("%s = %d", m, w.Code)
		}
	}
	if a.Providers["whoop"].Config().ClientID != "from-env" {
		t.Fatal("manifest credentials changed")
	}
}
