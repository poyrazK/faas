package runnerparity

// TestRunners_InvokeHandlerUsesCmdRun pins the legacy fallback in §4.9.1:
// every current runner retains a `cmd.Run()` path for customer handlers that
// do not advertise the generated persistent protocol. Generated adapters use
// the worker pool, but the compatibility path must remain available.
//
// Every runner delegates to the shared internal.Serve (ADR-999), so the pin
// checks that each guest/runners/<runtime>/main.go delegates and that
// internal/serve.go keeps the one-process-per-request path:
//  1. invokeOnce exists
//  2. invokeOnce calls cmd.Run()
//
// We do not parse the Go AST — file-path string-matching is
// sufficient for this pin, and the test stays a one-file, no-
// dependency assertion that runs in microseconds.

import (
	"strings"
	"testing"
)

// runnerRoot is the parent of the runner sub-packages. Computed
// relative to this test file: ../../.. walks out of
// guest/runners/internal/runnerparity to the guest/ root, then
// ../runners lands in guest/runners. Tests run with cwd at the
// package directory, so this relative path resolves correctly.
const runnerRoot = "../../.."

func TestRunners_InvokeHandlerUsesCmdRun(t *testing.T) {
	names := requireRunnersDelegate(t, runnerMains(t))
	serve := serveSource(t)
	if !strings.Contains(serve, "func invokeOnce") {
		t.Fatal("internal/serve.go: missing invokeOnce (runner shape changed — update §4.9.1 and this test)")
	}
	if !strings.Contains(serve, "cmd.Run()") {
		t.Fatal("internal/serve.go: invokeOnce does not call cmd.Run() — runners switched to long-lived handlers only (closes the §4.9.1 listener-vs-handler gap, update spec)")
	}
	t.Logf("walked %d runner dirs %v — all delegate to internal.Serve, which retains the cmd.Run fallback", len(names), names)
}
