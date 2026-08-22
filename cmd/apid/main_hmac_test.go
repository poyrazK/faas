// main_hmac_test.go — pins cmd/apid/main.go::loadHostHMACKey.
//
// 5 test functions cover the load-bearing refusal posture for
// /etc/faas/secrets/host.hmac.key (ADR-117 PR-C):
//
//  1. OK_400 — 32-byte file with mode 0o400 is loaded and the
//     returned slice is a defensive copy (not a reference to
//     the on-disk bytes).
//  2. OK_440 — mode 0o440 is accepted (the systemd-credentials
//     posture; group-readable when apid runs as a non-root group
//     member).
//  3. RejectsPermissiveMode — modes 0o600 / 0o640 / 0o644 / 0o660
//     / 0o664 / 0o666 / 0o777 are all rejected. The per-host HMAC
//     key is single-tenant per host, and a permissive mode would
//     let a non-root reader compute value_hash for any observed
//     plaintext.
//  4. RejectsWrongLength — 0/1/16/31/33/64/128-byte files are all
//     rejected. The helper is HMAC-SHA256, which accepts any key
//     length, but the column shape
//     (app_secrets_value_hash_shape, length <= 16 hex chars) and
//     the per-host trust boundary anchor the contract at 32.
//  5. RejectsMissingFile — a missing file is rejected with a
//     `bootstrap with ...` hint so the operator gets the exact
//     one-liner they need to run.

package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestLoadHostHMACKey_OK_400(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "host.hmac.key")
	if err := os.WriteFile(path, make([]byte, 32), 0o400); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := loadHostHMACKey(path)
	if err != nil {
		t.Fatalf("loadHostHMACKey: %v", err)
	}
	if len(got) != 32 {
		t.Errorf("len: got %d, want 32", len(got))
	}
	for _, b := range got {
		if b != 0 {
			t.Errorf("expected zero bytes from a fresh zeroed key, got %d at some position", b)
			break
		}
	}

	// Copy semantics: mutating the returned slice must not affect
	// a subsequent call's result.
	got[0] = 0xAB
	got2, err := loadHostHMACKey(path)
	if err != nil {
		t.Fatalf("loadHostHMACKey (2): %v", err)
	}
	if got2[0] != 0 {
		t.Errorf("returned slice is not a copy; second call sees caller mutation: got2[0] = %d", got2[0])
	}
}

func TestLoadHostHMACKey_OK_440(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "host.hmac.key")
	if err := os.WriteFile(path, make([]byte, 32), 0o440); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := loadHostHMACKey(path); err != nil {
		t.Errorf("loadHostHMACKey (0o440): %v (the systemd-credentials posture MUST be accepted; the perm check accepts 0o400 OR 0o440 only)", err)
	}
}

func TestLoadHostHMACKey_RejectsPermissiveMode(t *testing.T) {
	for _, mode := range []os.FileMode{0o600, 0o640, 0o644, 0o660, 0o664, 0o666, 0o777} {
		t.Run("0o"+modeToOctal(mode), func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "host.hmac.key")
			if err := os.WriteFile(path, make([]byte, 32), mode); err != nil {
				t.Fatalf("write: %v", err)
			}
			_, err := loadHostHMACKey(path)
			if err == nil {
				t.Errorf("mode 0o%o accepted; MUST be rejected (permissive mode lets a non-root reader compute value_hash)", mode)
				return
			}
			if !strings.Contains(err.Error(), "insecure file mode") {
				t.Errorf("error %q does not mention 'insecure file mode'; the message must guide the operator to chmod 0400", err)
			}
		})
	}
}

func TestLoadHostHMACKey_RejectsWrongLength(t *testing.T) {
	for _, n := range []int{0, 1, 16, 31, 33, 64, 128} {
		t.Run("len="+strconv.Itoa(n), func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "host.hmac.key")
			if err := os.WriteFile(path, make([]byte, n), 0o400); err != nil {
				t.Fatalf("write: %v", err)
			}
			_, err := loadHostHMACKey(path)
			if err == nil {
				t.Errorf("len=%d accepted; MUST be rejected (the per-host HMAC key is exactly 32 bytes by contract)", n)
				return
			}
			if !strings.Contains(err.Error(), "32") {
				t.Errorf("error %q does not mention '32'; the message must guide the operator to regenerate with 32 bytes", err)
			}
		})
	}
}

func TestLoadHostHMACKey_RejectsMissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "host.hmac.key")

	_, err := loadHostHMACKey(path)
	if err == nil {
		t.Fatal("missing file accepted; MUST be rejected (the apid must not start without the per-host HMAC key)")
	}
	if !strings.Contains(err.Error(), "openssl rand") {
		t.Errorf("error %q does not include the bootstrap one-liner; the message must guide the operator to `openssl rand -out ... 32 && chmod 0400 ...`", err)
	}
}

// modeToOctal renders a file mode as a 3-digit octal string.
// Local to this test so we don't pull strconv into the production
// file just for diagnostics.
func modeToOctal(m os.FileMode) string {
	const digits = "01234567"
	v := uint32(m.Perm())
	return string([]byte{
		digits[(v>>6)&0o7],
		digits[(v>>3)&0o7],
		digits[v&0o7],
	})
}
