package provider

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// Credentials entered in Settings are kept as owner-only files under
// OUTPOST_STORAGE_DIR/secrets, one pair per provider, so they ride along with
// backup and restore like the OAuth tokens do. Credentials named in the
// manifest (LoadConfig) take precedence and can't be changed from the app.

// Where a provider's client credentials came from.
const (
	FromNone     = ""
	FromManifest = "manifest"
	FromSettings = "settings"
)

// SecretsDir is where credentials entered in Settings are kept.
func SecretsDir(storage string) string { return filepath.Join(storage, "secrets") }

func credPaths(dir, name string) (id, secret string) {
	return filepath.Join(dir, name+"-client-id"), filepath.Join(dir, name+"-client-secret")
}

// LoadSaved reads credentials saved from Settings. Neither file existing
// means none were saved; one without the other is an error.
func LoadSaved(dir, name string) (id, secret string, err error) {
	ip, sp := credPaths(dir, name)
	_, ie := os.Stat(ip)
	_, se := os.Stat(sp)
	if errors.Is(ie, os.ErrNotExist) && errors.Is(se, os.ErrNotExist) {
		return "", "", nil
	}
	if id, err = readSecretFile(name+" client ID", ip); err != nil {
		return "", "", err
	}
	if secret, err = readSecretFile(name+" client secret", sp); err != nil {
		return "", "", err
	}
	return id, secret, nil
}

// SaveCredentials writes both files atomically with mode 600 in a 700
// directory.
func SaveCredentials(dir, name, id, secret string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	ip, sp := credPaths(dir, name)
	if err := writePrivate(sp, secret); err != nil {
		return err
	}
	return writePrivate(ip, id)
}

// DeleteCredentials removes saved credentials; missing files are fine.
func DeleteCredentials(dir, name string) error {
	ip, sp := credPaths(dir, name)
	for _, p := range []string{sp, ip} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func writePrivate(path, v string) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // no-op after a successful rename
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.WriteString(v + "\n"); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// CheckCredential validates a pasted client ID or secret: printable ASCII,
// no spaces, a sane length. Errors never echo the value.
func CheckCredential(label, v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", fmt.Errorf("%s is empty", label)
	}
	if len(v) > 512 {
		return "", fmt.Errorf("%s is too long to be right", label)
	}
	for _, r := range v {
		if r > unicode.MaxASCII || !unicode.IsPrint(r) || unicode.IsSpace(r) {
			return "", fmt.Errorf("%s has a space or an unexpected character; paste it again", label)
		}
	}
	return v, nil
}
