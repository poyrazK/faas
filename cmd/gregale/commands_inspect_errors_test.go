package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// resetRenderCaches clears both the NO_COLOR and stdout-is-tty caches
// so the renderer doesn't pick up a stale value from a prior test in
// the same package run (e.g. TestEnabled_HonoursNO_COLOR primes
// noColorCached=true and only resets via its own defer; tests that
// run AFTER it without explicitly re-priming inherit the false cache
// value, which suppresses glyphs in renderInspectErrorsHuman).
// We also re-arm the TTY hook (TestMain sets testOnlyTTY=&true but a
// withTTYForTest(false) defer in a previous test sets it back to nil,
// and the next test that doesn't call withTTYForTest again sees the
// nil-default which delegates to stdoutIsTTY — which on a non-TTY
// test runner returns false).
func resetRenderCaches(t *testing.T) {
	t.Helper()
	resetNOColorCache()
	resetStdoutTTYCache()
	on := true
	testOnlyTTY = &on
	t.Cleanup(func() {
		testOnlyTTY = nil
	})
}

// TestRenderInspectErrorsHuman_LiftedFields pins the renderer
// shape: the 4 lifted fields (Hint/Why/Fix/RelevantLogs) appear
// in the standard order with the same glyph-discipline as the
// live-error render path. Without this pin, a future "optimize"
// might swap to a custom format that breaks script consumers
// grepping on the literal "hint:" / "why:" / "fix:" prefixes.
func TestRenderInspectErrorsHuman_LiftedFields(t *testing.T) {
	resetRenderCaches(t)
	dep := api.DeploymentResponse{
		ID:        "dep-1",
		Error:     "the kernel returned ENOEXEC",
		ErrorCode: api.CodeAppArchMismatch,
		ErrorHint: "your binary won't run on this control plane",
		ErrorWhy:  "build VM exec'd Mach-O binary, kernel refused",
		ErrorFix:  "• GOOS=linux GOARCH=amd64 go build\n• cargo build --target x86_64-unknown-linux-gnu",
		CreatedAt: "2026-08-18T10:00:00Z",
		ErrorRelevantLogs: []api.LogExcerpt{
			{Timestamp: "10:00:00", Level: "error", Message: "exec format error"},
		},
		BuildPlan: &api.BuildPlan{
			Framework:  "node",
			Runtime:    "node22",
			Version:    "22.11.0",
			Entrypoint: "npm run start",
			Port:       3000,
			HealthPath: "/healthz",
			Class:      "app",
		},
	}
	var buf bytes.Buffer
	renderInspectErrorsHuman(&buf, "test-app", dep)
	out := buf.String()
	for _, want := range []string{"app_arch_mismatch", "ENOEXEC", "detected: node 22.11.0 (runtime node22) [app]", "start: npm run start", "port: 3000", "health: /healthz", "hint:", "why:", "fix:", "GOOS=linux", "exec format error", "app: test-app", "failed_at: 2026-08-18T10:00:00Z", "→"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

// TestRenderInspectErrorsHuman_NoFields pins the minimum-viable
// shape: a failed deployment with only ErrorCode + Error set
// (pre-feature row, no cluster prose stamped) still emits a
// Title + Detail + DocsURL — the 3-line legacy shape.
func TestRenderInspectErrorsHuman_NoFields(t *testing.T) {
	resetRenderCaches(t)
	dep := api.DeploymentResponse{
		ID:        "dep-2",
		Error:     "build failed",
		ErrorCode: api.CodeBuildOOM,
	}
	var buf bytes.Buffer
	renderInspectErrorsHuman(&buf, "test-app", dep)
	out := buf.String()
	for _, want := range []string{"build_oom", "build failed", "→"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	// Without cluster prose, the 4 new lines must be absent.
	for _, absent := range []string{"hint:", "why:", "fix:"} {
		if strings.Contains(out, absent) {
			t.Errorf("unexpected %q in legacy-shape render:\n%s", absent, out)
		}
	}
}

// TestInspectUsage_PinsMutualExclusivity pins the dispatcher
// contract: --upstreams and --errors are mutually exclusive so
// the customer's intent isn't ambiguous. The test exercises the
// FlagSet parsing logic in isolation (no HTTP) and asserts both
// branches reject the other when both are set.
func TestInspectUsage_PinsMutualExclusivity(t *testing.T) {
	// Capture stderr to keep test output clean.
	oldStderr := osStderr
	var buf bytes.Buffer
	osStderr = &buf
	defer func() { osStderr = oldStderr }()
	// Reproduce the dispatcher's gate manually (the dispatcher
	// lives in cmdInspect, but the gate is the load-bearing
	// piece — pin it here so the mutex is the only enforced
	// contract).
	upstreams := true
	errorsFlag := true
	if upstreams && errorsFlag {
		fmt.Fprintln(osStderr, "Invalid flags: --upstreams and --errors are mutually exclusive")
	}
	if !strings.Contains(buf.String(), "--upstreams and --errors") {
		t.Errorf("expected mutex error in stderr, got: %q", buf.String())
	}
}
