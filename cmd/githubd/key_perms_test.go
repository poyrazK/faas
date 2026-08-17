package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/secretbox"
)

// TestReadKeyPEMDefault_RejectsLooseMode is the issue #603 acceptance
// case: a 0644 GitHub App private key must stop githubd, not be read.
// The key mints installation tokens for every customer repo the App
// is installed on, so a world-readable copy is a tenant-wide leak.
func TestReadKeyPEMDefault_RejectsLooseMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "github-app.pem")
	if err := os.WriteFile(path, []byte("-----BEGIN PRIVATE KEY-----\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Setenv("FAAS_GITHUB_APP_KEY_PATH", path)

	got, err := readKeyPEMDefault()
	if err == nil {
		t.Fatalf("0644 key must be refused, got %d bytes", len(got))
	}
	if !errors.Is(err, secretbox.ErrInsecureFileMode) {
		t.Errorf("error %v does not wrap ErrInsecureFileMode", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error %q does not name the offending path", err)
	}
	if got != nil {
		t.Error("no key material may be returned alongside the error")
	}
}

// TestReadKeyPEMDefault_AcceptsProvisionedMode pins the happy path so
// the new gate cannot regress into refusing the mode bootstrap
// actually writes (0400 per spec §11).
func TestReadKeyPEMDefault_AcceptsProvisionedMode(t *testing.T) {
	const pem = "-----BEGIN PRIVATE KEY-----\nabc\n-----END PRIVATE KEY-----\n"
	for _, mode := range []os.FileMode{0o400, 0o440, 0o600, 0o640} {
		path := filepath.Join(t.TempDir(), "github-app.pem")
		if err := os.WriteFile(path, []byte(pem), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		if err := os.Chmod(path, mode); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		t.Setenv("FAAS_GITHUB_APP_KEY_PATH", path)

		got, err := readKeyPEMDefault()
		if err != nil {
			t.Fatalf("mode %#o must be accepted: %v", mode, err)
		}
		if string(got) != pem {
			t.Errorf("mode %#o: key material round-trip mismatch", mode)
		}
	}
}
