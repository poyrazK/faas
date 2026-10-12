package internal

// Issue #667 / ADR-078 (PR 3) — checkbox #6 acceptance test:
// "pathological tail killed at the ceiling."
//
// The runner's tail host is the per-request pump that drains
// envelope.TailPipePath and emits one 0x04 DGRAM per task via
// the tail-events proxy. The acceptance criterion is that the
// per-task context.WithTimeout ceiling actually fires for a
// pathological tail — a customer's promise that hangs past
// WaitUntilSec must produce a Timeout outcome on the wire, not
// hang forever (and not be silently marked Completed).
//
// Why this lives in the internal package, not in any runner's
// main_test.go: the runner core's tail drain registers a no-op
// taskFn (the customer's promise is opaque to the runner; the
// runner only observes the timeout). The pathological-tail
// assertion is about the TailHost's goroutine behavior on a
// hanging taskFn — the runner wires the host, the host enforces
// the per-task ceiling. Testing the host directly pins the
// behavior without paying the runner's subprocess-spawn cost
// (the runner tests already cover the wiring).
//
// Test plan:
//
//  1. Construct a TailHost with waitUntilSec=1, TailCapMax=1.
//  2. Register a task whose taskFn blocks on <-ctx.Done()
//     (the customer's promise is hanging).
//  3. Measure wall time from Register() to Drain() return.
//  4. Assert wall time is bounded by waitUntil + slack
//     (the 250ms TailWriteTimeout).
//  5. Assert Failures() contains a "timeout:task-N" entry —
//     this is the load-bearing assertion that the per-task
//     context fired, not the outer 5s snapshotAndPark.
//
// We do NOT exercise the 0x04 DGRAM emit here (the proxy isn't
// running in unit tests). The runTask path emits via emit() and
// logs the failure to stderr if the dial fails — so the test
// doesn't gate on the proxy. The behavior we pin is the
// per-task timeout's interaction with the runner's drain.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTailHost_PathologicalTailKilledAtCeiling(t *testing.T) {
	const (
		waitUntilSec = 1
		slackBound   = 350 * time.Millisecond // 250ms TailWriteTimeout + 100ms CI jitter
	)

	// The pipe path is read by TailHost.ReadPipe (which gates
	// on os.IsNotExist), so an empty path is fine for this
	// test — the taskFn is a hang, not a pipe-driven test.
	// The pipe is registered but ReadPipe is not called here;
	// the test directly uses Register + Drain.
	pipePath := filepath.Join(t.TempDir(), "tail.jsonl")
	host := NewTailHost("go124-test", pipePath, waitUntilSec, 1)

	taskID := "task-1"
	hangFn := func(ctx context.Context) {
		// Customer's promise hangs forever. The runner's
		// per-task context.WithTimeout should fire and
		// cancel this goroutine.
		<-ctx.Done()
	}

	start := time.Now()
	host.Register(context.Background(), taskID, hangFn)
	host.Drain()
	elapsed := time.Since(start)

	// 1. Wall-clock duration is bounded by WaitUntilSec + slack.
	// The drain must finish even though the taskFn is hanging.
	ceiling := time.Duration(waitUntilSec)*time.Second + slackBound
	if elapsed >= ceiling {
		t.Fatalf("drain ran %v, want < %v (waitUntilSec=%d + slack=%v)",
			elapsed, ceiling, waitUntilSec, slackBound)
	}
	// And it must NOT have returned instantly — that would mean
	// the per-task timeout didn't fire and the WaitGroup
	// unblocked (impossible with a hang taskFn, but the floor
	// assertion pins the test logic).
	if elapsed < time.Duration(waitUntilSec)*time.Second {
		t.Fatalf("drain returned in %v, want >= %v (taskFn must have been cancelled by the per-task timeout)",
			elapsed, time.Duration(waitUntilSec)*time.Second)
	}

	// 2. Failures() contains "timeout:task-1" — the per-task
	// timeout fired.
	failures := host.Failures()
	if len(failures) != 1 {
		t.Fatalf("failures = %+v, want exactly 1 entry (timeout:task-1)", failures)
	}
	if !strings.Contains(failures[0], "timeout:task-1") {
		t.Fatalf("failure = %q, want substring 'timeout:task-1'", failures[0])
	}
}

// TestTailHost_DrainReturnsImmediatelyOnEmpty confirms the
// happy-path: no registered tasks → Drain() returns in ≪ waitUntil.
// A Drain that hangs on an empty WaitGroup would starve the
// runner's request-handling path. Load-bearing for the
// 95% case (handlers that don't use waitUntil at all).
func TestTailHost_DrainReturnsImmediatelyOnEmpty(t *testing.T) {
	host := NewTailHost("go124-test", "", 30, 1)
	start := time.Now()
	host.Drain()
	elapsed := time.Since(start)
	if elapsed > 50*time.Millisecond {
		t.Fatalf("empty drain took %v, want < 50ms", elapsed)
	}
}

// TestTailHost_RegisterHonorsTailCapMax pins the structural cap
// (issue #667 / ADR-078: TailCapMax = 16, pinned in
// pkg/api/limits.go). The 17th registration must be rejected; the
// caller drops the tail and bumps the wire counter
// (pkg/wire/metrics.TailCapReached). The test confirms the host
// returns false from Register() at the cap boundary.
func TestTailHost_RegisterHonorsTailCapMax(t *testing.T) {
	const cap = 4
	host := NewTailHost("go124-test", "", 10, cap)
	for i := 0; i < cap; i++ {
		taskID := fmt.Sprintf("task-%d", i)
		if !host.Register(context.Background(), taskID, func(_ context.Context) {}) {
			t.Fatalf("register %d/%d returned false; want true", i+1, cap)
		}
	}
	// The cap-th+1 registration must be rejected.
	if host.Register(context.Background(), "task-overflow", func(_ context.Context) {}) {
		t.Fatal("register over cap returned true; want false (TailCapMax enforced)")
	}
	if got := host.RegisterCount(); got != cap {
		t.Fatalf("RegisterCount = %d, want %d", got, cap)
	}
}

// TestTailHost_DrainSafetyNetTimeoutFires pins the safety-net
// timeout (the ctx.Done() branch in Drain) on a pathological
// taskFn that ignores ctx.Done() — the drain must NOT block past
// waitUntil + TailWriteTimeout. This is the *outer* drain bound
// (vs. the per-task context.WithTimeout bound in runTask). The
// runner's drain respects both; the schedd's 5s snapshotAndPark
// watchdog is the *park* bound, not the *drain* bound.
func TestTailHost_DrainSafetyNetTimeoutFires(t *testing.T) {
	const waitUntilSec = 1
	// 350ms slack = TailWriteTimeout (250ms) + 100ms CI jitter.
	const slackBound = 350 * time.Millisecond
	host := NewTailHost("go124-test", "", waitUntilSec, 1)
	hangFn := func(ctx context.Context) {
		// Don't honor ctx.Done() — the runner's per-task
		// timeout will fire after waitUntil, but the drain's
		// own outer timeout (waitUntil + TailWriteTimeout)
		// is the safety net if it doesn't.
		time.Sleep(10 * time.Second)
	}
	host.Register(context.Background(), "task-1", hangFn)
	start := time.Now()
	host.Drain()
	elapsed := time.Since(start)
	ceiling := time.Duration(waitUntilSec)*time.Second + slackBound
	if elapsed >= ceiling {
		t.Fatalf("drain ran %v, want < %v (outer timeout bound by waitUntil + TailWriteTimeout)",
			elapsed, ceiling)
	}
}

// TestReadPipe_NoFile pins the "no pipe" path — the customer
// never called waitUntil, so the pipe doesn't exist. ReadPipe
// silently returns nil (no error). The runner keeps draining.
func TestReadPipe_NoFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist.jsonl")
	if err := ReadPipe(missing, func(line TailLine) {
		t.Errorf("onLine called for non-existent pipe: %+v", line)
	}); err != nil {
		t.Fatalf("ReadPipe on missing file = %v, want nil", err)
	}
}

// TestReadPipe_ParsesLines confirms the runner can read the
// JSONL shape the customer's waitUntil shim writes — one
// JSON object per line. A malformed line is silently dropped
// (the runner keeps draining on a partial read).
func TestReadPipe_ParsesLines(t *testing.T) {
	dir := t.TempDir()
	pipe := filepath.Join(dir, "tail.jsonl")
	content := `{"id":"t-1","wait":false}
not-json-line
{"id":"t-2","wait":true,"err":""}
{"id":"","wait":false}    (empty id — dropped, no callback)
`
	if err := os.WriteFile(pipe, []byte(content), 0o644); err != nil {
		t.Fatalf("write pipe: %v", err)
	}
	var got []string
	if err := ReadPipe(pipe, func(line TailLine) {
		got = append(got, line.ID)
	}); err != nil {
		t.Fatalf("ReadPipe: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d lines, want 2 (t-1, t-2): %v", len(got), got)
	}
	if got[0] != "t-1" || got[1] != "t-2" {
		t.Fatalf("got = %v, want [t-1, t-2]", got)
	}
}
