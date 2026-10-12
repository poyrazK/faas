// session_key_loader_test.go — regression sentinel for the
// FAAS_SESSION_KEY env-var SHAPE contract (issue #585 / ADR-127).
//
// The apid unit (deploy/systemd/faas-apid.service) wires FAAS_SESSION_KEY
// via LoadCredential=faas_session_key:/etc/faas/secrets/session.key +
// Environment=FAAS_SESSION_KEY=%d/faas_session_key — i.e. PATH-shaped
// delivery: the env var holds a tmpfs path, the loader must read the
// file and then decode its content. Before PR #1075 review-fix the
// loader did hex.DecodeString(env) directly, which silently fell back
// to NewEphemeralManager (the A5 silent-degradation bug closed by this
// PR). These tests pin both shapes:
//   - PATH-shaped: env var = path to a 0400 root:root file containing
//     64 hex chars; loader must ReadFile and decode.
//   - CONTENT-shaped: env var = raw 64 hex chars (e2e tests, dev);
//     loader must decode in place.
//   - Empty env: dev fallback to NewEphemeralManager + warning string.
//   - Path pointing to a non-file: fall through to CONTENT-shaped
//     decoder so a typo doesn't crash boot.
//   - Hex with wrong byte length: fail-closed (no silent fallback).
//
// Mirror in cmd/gatewayd-internal/session_key_loader_test.go (the
// gatewayd-internal loader is byte-for-byte the same env contract;
// the fix ships together so a future regression on either side
// gets caught).

package apidserver

import (
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testHexKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

// TestSessionManager_PATH_ReadFileFails exercises the new
// "FAAS_SESSION_KEY path read failed" sentinel — the branch that fires
// when os.Stat succeeds (file exists, IsRegular) but os.ReadFile
// fails (perms revoked mid-boot, file replaced by a symlink-to-
// nothing between Stat and ReadFile, EIO on a tmpfs credential).
//
// Without this test the only thing pinning that branch is the
// TestSessionManager_PATH_DoesNotExist_FailsClosed test, which
// actually falls through to the hex decoder (os.Stat returns ENOENT,
// so the path branch never runs). chmod 0000 is portable across
// Linux/macOS but is bypassed by uid 0; on root we Skip with a clear
// reason rather than falsely-green the test.
func TestSessionManager_PATH_ReadFileFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod 0000 is bypassed by uid 0; cannot exercise os.ReadFile EACCES in this sandbox")
	}
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "session.key")
	if err := os.WriteFile(keyPath, []byte(testHexKey), 0o400); err != nil {
		t.Fatalf("write key file: %v", err)
	}
	if err := os.Chmod(keyPath, 0o000); err != nil {
		t.Fatalf("chmod 0000: %v", err)
	}
	getenv := func(name string) string {
		if name == "FAAS_SESSION_KEY" {
			return keyPath
		}
		return ""
	}
	mgr, warning := loadSessionManager(getenv, newTestLogger())
	if mgr != nil {
		t.Errorf("readfail contract: want nil manager (fail-closed), got %+v", mgr)
	}
	if warning != "FAAS_SESSION_KEY path read failed" {
		t.Errorf("readfail contract: want path-read-failed sentinel, got %q", warning)
	}
}

// TestSessionManager_PATH_Shape is the load-bearing tripwire: the
// apid unit ships FAAS_SESSION_KEY=%d/faas_session_key, which on the
// box evaluates to /run/credentials/faas-apid.service/faas_session_key.
// The loader MUST os.ReadFile that path and decode the file content
// as hex. Without this branch, hex.DecodeString("/run/credentials/...")
// fails, log.Error fires, FAAS_SESSION_KEY invalid returns — every
// authenticated dashboard cookie silently 403s in production.
func TestSessionManager_PATH_Shape(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "session.key")
	if err := os.WriteFile(keyPath, []byte(testHexKey+"\n"), 0o400); err != nil {
		t.Fatalf("write key file: %v", err)
	}
	getenv := func(name string) string {
		if name == "FAAS_SESSION_KEY" {
			return keyPath
		}
		return ""
	}
	mgr, warning := loadSessionManager(getenv, newTestLogger())
	if mgr == nil {
		t.Fatalf("path-shaped contract: want manager, got nil warning=%q", warning)
	}
	if warning != "" {
		t.Errorf("path-shaped contract: want empty warning, got %q", warning)
	}
}

// TestSessionManager_CONTENT_Shape is the back-compat tripwire: e2e
// tests (cmd/e2e/iam3_sessions_test.go:91) and dev tooling inline the
// hex directly. Loader must continue to decode raw hex content even
// after the new PATH-shaped branch was added.
func TestSessionManager_CONTENT_Shape(t *testing.T) {
	getenv := func(name string) string {
		if name == "FAAS_SESSION_KEY" {
			return testHexKey
		}
		return ""
	}
	mgr, warning := loadSessionManager(getenv, newTestLogger())
	if mgr == nil {
		t.Fatalf("content-shaped contract: want manager, got nil warning=%q", warning)
	}
	if warning != "" {
		t.Errorf("content-shaped contract: want empty warning, got %q", warning)
	}
}

// TestSessionManager_PATH_DoesNotExist_FailsClosed pins the fail-closed
// behaviour for a misconfigured env: a path-shaped env var pointing at
// a nonexistent file (typo, race with bootstrap, broken LoadCredential)
// must NOT silently fall back to NewEphemeralManager. The loader hits
// hex.DecodeString against the path string, fails closed, returns
// the "FAAS_SESSION_KEY invalid" sentinel that apid's main.go uses to
// refuse boot. (Dev/operators see a clear log.Error instead of a
// silently-degraded cookie surface.)
func TestSessionManager_PATH_DoesNotExist_FailsClosed(t *testing.T) {
	getenv := func(name string) string {
		if name == "FAAS_SESSION_KEY" {
			// leading "/" so it triggers the path branch, but file
			// doesn't exist on disk
			return "/var/empty/this/does/not/exist"
		}
		return ""
	}
	mgr, warning := loadSessionManager(getenv, newTestLogger())
	if mgr != nil {
		t.Errorf("ghost-path contract: want nil manager (fail-closed), got %+v", mgr)
	}
	if warning != "FAAS_SESSION_KEY invalid" {
		t.Errorf("ghost-path contract: want invalid warning, got %q", warning)
	}
}

// TestSessionManager_Empty_FallsBackEphemeral pins the dev fallback:
// an empty env value must produce a real manager (NewEphemeralManager)
// so dev boxes without /etc/faas/secrets/session.key boot.
func TestSessionManager_Empty_FallsBackEphemeral(t *testing.T) {
	getenv := func(string) string { return "" }
	mgr, warning := loadSessionManager(getenv, newTestLogger())
	if mgr == nil {
		t.Fatalf("empty contract: want ephemeral manager, got nil warning=%q", warning)
	}
	if !strings.Contains(warning, "ephemeral") {
		t.Errorf("empty contract: want ephemeral warning, got %q", warning)
	}
}

// TestSessionManager_WrongByteLength_FailsClosed pins the A5 fix:
// a 64-char-but-not-32-byte hex string must NOT silently fall back
// to NewEphemeralManager. Fail-closed (the apid boot path checks the
// warning string and aborts with a clear log line).
func TestSessionManager_WrongByteLength_FailsClosed(t *testing.T) {
	// 16 bytes hex → 32 chars (not 64)
	shortHex := hex.EncodeToString([]byte("0123456789abcdef"))
	getenv := func(name string) string {
		if name == "FAAS_SESSION_KEY" {
			return shortHex
		}
		return ""
	}
	mgr, warning := loadSessionManager(getenv, newTestLogger())
	if mgr != nil {
		t.Errorf("short-hex contract: want nil manager (fail-closed), got %+v", mgr)
	}
	if warning != "FAAS_SESSION_KEY invalid" {
		t.Errorf("short-hex contract: want invalid warning, got %q", warning)
	}
}

// TestSessionManager_PATH_NoNewlineBytes pins the canonical-file tripwire:
// the production session.key file emitted by `gregale secrets init`
// ends with no newline; the loader must trim before hex.DecodeString
// so a trailing '\n' from a hand-edited file doesn't fail.
func TestSessionManager_PATH_NoNewlineBytes(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "session.key")
	if err := os.WriteFile(keyPath, []byte(testHexKey), 0o400); err != nil {
		t.Fatalf("write key file: %v", err)
	}
	getenv := func(name string) string {
		if name == "FAAS_SESSION_KEY" {
			return keyPath
		}
		return ""
	}
	mgr, warning := loadSessionManager(getenv, newTestLogger())
	if mgr == nil {
		t.Fatalf("path-no-newline contract: want manager, got nil warning=%q", warning)
	}
}
