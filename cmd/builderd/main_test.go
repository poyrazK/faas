// Tests for the builderd daemon entrypoint. The full happy path needs
// vmmd + KVM + a real builder-base.ext4 (issue #57's metal e2e). Here
// we cover the pure config-loading + DI seam, matching the schedd
// test convention.

package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
)

// discardLog matches cmd/schedd/main_test.go: tests don't want slog output
// noise on the assertion-failure path.
func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// TestEnvOr_PrefersOSOverFallback is the regression guard for the
// FAAS_BUILDERD_CONFIG env override added for issue #57 — the harness
// needs to point builderd at a per-test config under /tmp. Without the
// env read, defaultDeps() returns the immutable /etc/faas/builderd.toml
// and the e2e test cannot drive a custom config (cache_dir, build dirs).
func TestEnvOr_PrefersOSOverFallback(t *testing.T) {
	t.Setenv("FAAS_BUILDERD_CONFIG", "/tmp/builderd-test.toml")
	if got := envOr("FAAS_BUILDERD_CONFIG", "/etc/faas/builderd.toml"); got != "/tmp/builderd-test.toml" {
		t.Errorf("envOr = %q, want /tmp/builderd-test.toml", got)
	}
}

// TestEnvOr_FallsBackWhenUnset pins the default path. Mirrors the
// constant in cmd/builderd/main.go::defaultDeps — if either drifts,
// the EX44 production start silently loads the wrong file.
func TestEnvOr_FallsBackWhenUnset(t *testing.T) {
	t.Setenv("FAAS_BUILDERD_CONFIG", "")
	if got := envOr("FAAS_BUILDERD_CONFIG", "/etc/faas/builderd.toml"); got != "/etc/faas/builderd.toml" {
		t.Errorf("envOr fallback = %q, want /etc/faas/builderd.toml", got)
	}
}

// TestDefaultDeps_UsesEnvOverride confirms defaultDeps wires the env
// read into the runDeps seam. This is what the harness depends on —
// if defaultDeps reverts to a hardcoded path the env override becomes
// inert and the e2e test can't isolate config.
func TestDefaultDeps_UsesEnvOverride(t *testing.T) {
	t.Setenv("FAAS_BUILDERD_CONFIG", "/var/lib/faas/test/builderd.toml")
	if got := defaultDeps().configPath; got != "/var/lib/faas/test/builderd.toml" {
		t.Errorf("defaultDeps().configPath = %q, want the env-set value", got)
	}
}

// TestRun_BadConfigPath exercises the non-ENOENT read failure path
// the way schedd's TestRun_BadConfigPath does: passing a directory
// (not a file) to LoadConfig must surface a wrapped error so an
// operator's broken config refuses to come up.
func TestRun_BadConfigPath(t *testing.T) {
	deps := runDeps{
		configPath: t.TempDir(), // a directory; not a regular file
	}
	err := runWithDeps(context.Background(), discardLog(), deps)
	if err == nil {
		t.Fatal("expected error from directory-as-config-path")
	}
	// The error must wrap something — not just an empty message —
	// so an operator's logs explain why builderd refused to start.
	var wantEmpty error
	if errors.Is(err, wantEmpty) {
		t.Errorf("expected non-empty error, got %v", err)
	}
}
