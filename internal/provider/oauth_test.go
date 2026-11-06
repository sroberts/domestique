package provider

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSecret(t *testing.T, body string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil { // WriteFile's mode is masked by umask
		t.Fatal(err)
	}
	return p
}

func TestLoadConfigSecrets(t *testing.T) {
	const pre = "DOMESTIQUE_TEST"
	cases := []struct {
		name    string
		env     map[string]string
		wantErr string
		secret  string
	}{
		{name: "not configured", env: map[string]string{}},
		{name: "secret from file", env: map[string]string{pre + "_CLIENT_ID": "id", pre + "_CLIENT_SECRET_FILE": writeSecret(t, "s3cret\n", 0o600)}, secret: "s3cret"},
		{name: "plain env secret refused", env: map[string]string{pre + "_CLIENT_ID": "id", pre + "_CLIENT_SECRET": "s3cret"}, wantErr: "_CLIENT_SECRET_FILE"},
		{name: "plain env secret refused even unconfigured", env: map[string]string{pre + "_CLIENT_SECRET": "s3cret"}, wantErr: "_CLIENT_SECRET_FILE"},
		{name: "id without secret file", env: map[string]string{pre + "_CLIENT_ID": "id"}, wantErr: "isn't"},
		{name: "world-readable file", env: map[string]string{pre + "_CLIENT_ID": "id", pre + "_CLIENT_SECRET_FILE": writeSecret(t, "s3cret", 0o644)}, wantErr: "chmod 600"},
		{name: "empty file", env: map[string]string{pre + "_CLIENT_ID": "id", pre + "_CLIENT_SECRET_FILE": writeSecret(t, " \n", 0o600)}, wantErr: "empty"},
		{name: "missing file", env: map[string]string{pre + "_CLIENT_ID": "id", pre + "_CLIENT_SECRET_FILE": "/nonexistent/secret"}, wantErr: "no such file"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, k := range []string{"_CLIENT_ID", "_CLIENT_ID_FILE", "_CLIENT_SECRET", "_CLIENT_SECRET_FILE"} {
				t.Setenv(pre+k, "")
			}
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			c, err := LoadConfig(Config{Name: "test"}, pre)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				if strings.Contains(err.Error(), "s3cret") {
					t.Fatalf("error leaks the secret: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if c.ClientSecret != tc.secret {
				t.Fatalf("secret = %q, want %q", c.ClientSecret, tc.secret)
			}
		})
	}
}
