// session_key_loader_test.go — mirror of cmd/apid/session_key_loader_test.go.
// See that file for the rationale (issue #585 / ADR-127 review-fix R1+R2
// — FAAS_SESSION_KEY shape contract). The gatewayd-internal loader
// mirrors the apid env contract (PATH + CONTENT shapes; fail-closed on
// broken keys) so a regression on either side gets caught at unit-test
// time rather than at production 503 time. The 6 cases below are the
// apid set (PATH, CONTENT, ghost-path, empty-fallback, wrong-length,
// no-newline) minus the chmod-0000 ReadFile-fails case — the apid-only
// test runs as a uid check; mirror covers the equivalent shape.

package main

import (
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

const testHexKey = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"

func newTestLogger2() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

func TestGatewaydSessionManager_PATH_Shape(t *testing.T) {
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
	mgr := loadSessionManager(getenv, newTestLogger2())
	if mgr == nil {
		t.Fatal("path-shaped: want manager, got nil")
	}
}

func TestGatewaydSessionManager_CONTENT_Shape(t *testing.T) {
	getenv := func(name string) string {
		if name == "FAAS_SESSION_KEY" {
			return testHexKey
		}
		return ""
	}
	mgr := loadSessionManager(getenv, newTestLogger2())
	if mgr == nil {
		t.Fatal("content-shaped: want manager, got nil")
	}
}

func TestGatewaydSessionManager_PATH_DoesNotExist_FailsClosed(t *testing.T) {
	// Mirror of apid's same-named test: a path-shaped env var pointing
	// at a nonexistent file (typo, race with bootstrap, broken
	// LoadCredential) must NOT silently produce a manager. The
	// gatewayd loader falls through to hex.DecodeString of the path
	// string itself, which fails → mgr=nil (the canonical CONTENT
	// decoder fail-closed path).
	getenv := func(name string) string {
		if name == "FAAS_SESSION_KEY" {
			return "/var/empty/this/does/not/exist"
		}
		return ""
	}
	mgr := loadSessionManager(getenv, newTestLogger2())
	if mgr != nil {
		t.Errorf("ghost-path contract: want nil manager (fail-closed), got %+v", mgr)
	}
}

func TestGatewaydSessionManager_PATH_NoNewlineBytes(t *testing.T) {
	// Mirror of apid's canonical-file tripwire: the production
	// session.key emitted by `gregale secrets init` ends with no
	// trailing newline. The loader must trim before hex.DecodeString
	// so a hand-edited file with a trailing '\n' doesn't fail.
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
	mgr := loadSessionManager(getenv, newTestLogger2())
	if mgr == nil {
		t.Fatal("path-no-newline contract: want manager, got nil")
	}
}

func TestGatewaydSessionManager_Empty_FallsBackEphemeral(t *testing.T) {
	getenv := func(string) string { return "" }
	mgr := loadSessionManager(getenv, newTestLogger2())
	if mgr == nil {
		t.Fatal("empty contract: want ephemeral manager, got nil")
	}
	// Verify the warning path was logged — use a custom capture
	// because the gatewayd loader logs WARN before returning the
	// ephemeral manager.
	buf := &strings.Builder{}
	log := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	_ = loadSessionManager(func(string) string { return "" }, log)
	if !strings.Contains(buf.String(), "ephemeral session key in use") {
		t.Errorf("empty contract: want ephemeral warning log, got %q", buf.String())
	}
}

func TestGatewaydSessionManager_WrongByteLength_FailsClosed(t *testing.T) {
	shortHex := hex.EncodeToString([]byte("0123456789abcdef"))
	getenv := func(name string) string {
		if name == "FAAS_SESSION_KEY" {
			return shortHex
		}
		return ""
	}
	mgr := loadSessionManager(getenv, newTestLogger2())
	if mgr != nil {
		t.Errorf("short-hex: want nil manager, got %+v", mgr)
	}
}

func TestTrafficDeadlineSignerHasNoEphemeralFallback(t *testing.T) {
	signer, err := loadTrafficDeadlineSigner(func(string) string { return "" })
	if err != nil || signer != nil {
		t.Fatalf("signer=%v error=%v; want disabled without shared material", signer, err)
	}
}

func TestTrafficDeadlineSignerSharedContentAndPathKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.key")
	if err := os.WriteFile(path, []byte(testHexKey), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := loadTrafficDeadlineSigner(func(string) string { return testHexKey })
	if err != nil {
		t.Fatal(err)
	}
	second, err := loadTrafficDeadlineSigner(func(string) string { return path })
	if err != nil {
		t.Fatal(err)
	}
	token, err := first.Mint("app", "account", uuid.NewString(), time.Now().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.Verify(token, "app"); err != nil {
		t.Fatalf("separate loaders disagree: %v", err)
	}
	buf := &strings.Builder{}
	if manager := loadSessionManager(func(string) string { return path }, slog.New(slog.NewTextHandler(buf, nil))); manager == nil {
		t.Fatal("session manager failed")
	}
	if strings.Contains(buf.String(), testHexKey) {
		t.Fatal("loader logged secret material")
	}
}
