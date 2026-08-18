package secretbox

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAssertSecretFileMode pins the issue #603 allowlist: the four
// owner/group modes pass, everything else fails closed. The
// world-readable 0644 row is the one the issue was filed for — a
// stray umask during bootstrap.
func TestAssertSecretFileMode(t *testing.T) {
	cases := []struct {
		name    string
		mode    os.FileMode
		wantErr bool
	}{
		{name: "0400-owner-read", mode: 0o400},
		{name: "0440-owner-group-read", mode: 0o440},
		{name: "0600-owner-rw", mode: 0o600},
		{name: "0640-owner-rw-group-read", mode: 0o640},
		// The reported failure: readable by every user on the box.
		{name: "0644-world-readable", mode: 0o644, wantErr: true},
		{name: "0444-world-readable", mode: 0o444, wantErr: true},
		{name: "0660-group-writable", mode: 0o660, wantErr: true},
		{name: "0700-owner-exec", mode: 0o700, wantErr: true},
		{name: "0777-everything", mode: 0o777, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "secret.pem")
			if err := os.WriteFile(path, []byte("key"), 0o600); err != nil {
				t.Fatalf("write: %v", err)
			}
			// WriteFile's mode is subject to umask; Chmod is not.
			if err := os.Chmod(path, tc.mode); err != nil {
				t.Fatalf("chmod: %v", err)
			}
			err := AssertSecretFileMode("githubd", path)
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("mode %#o must be accepted, got %v", tc.mode, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("mode %#o must be rejected", tc.mode)
			}
			if !errors.Is(err, ErrInsecureFileMode) {
				t.Errorf("error %v does not wrap ErrInsecureFileMode", err)
			}
			// The operator reading a failed unit start needs the
			// daemon, the path, the observed mode and the fix.
			for _, want := range []string{"githubd", path, "chmod 0400"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
		})
	}
}

// TestAssertSecretFileMode_NonRegularAndMissing covers the two
// not-a-mode failures, which must stay distinguishable from an
// insecure mode so an operator can tell "didn't provision" apart from
// "provisioned insecurely".
func TestAssertSecretFileMode_NonRegularAndMissing(t *testing.T) {
	dir := t.TempDir()

	if err := AssertSecretFileMode("githubd", filepath.Join(dir, "absent.pem")); err == nil {
		t.Error("a missing file must be an error")
	} else if errors.Is(err, ErrInsecureFileMode) {
		t.Error("a missing file must NOT report as an insecure mode")
	}

	if err := AssertSecretFileMode("githubd", dir); err == nil {
		t.Error("a directory must be an error")
	} else if !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("error %q should say the path is not a regular file", err)
	}

	if err := AssertSecretFileMode("githubd", ""); err == nil {
		t.Error("an empty path must be an error")
	}
}

// TestAllowedSecretFileMode keeps the predicate and the list in
// agreement — a mode added to secretFileModes without thinking shows
// up here. cmd/gatewayd-internal/secrets.go::allowedSecretPerm
// delegates to this predicate, so a drift here moves two daemons at
// once.
func TestAllowedSecretFileMode(t *testing.T) {
	for _, m := range secretFileModes {
		if !AllowedSecretFileMode(m) {
			t.Errorf("secretFileModes contains %#o but the predicate rejects it", m)
		}
	}
	if AllowedSecretFileMode(0o644) {
		t.Error("0644 must never be allowed")
	}
}
