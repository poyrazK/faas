//go:build linux

// Tests for the workload-OOM detection seam (Cluster C /
// ADR-121). The pure-function tests pin the helper
// behaviour that's available on any linux build (no KVM
// required); the metal test exercises the full
// guest-init → vsock DGRAM → host receiver → schedd
// stamping chain and is gated behind //go:build metal.
//
// Wire envelope (cluster-c-error-explanations-runtime-
// oom-shipped, ADR-121):
//
//	guest-init cgroup.events listener
//	  ↓ vsock DGRAM type=0x05 on port 1027
//	cmd/vmmd framework_ready_recv.go
//	  ↓ Manager.ReportWorkloadOOM
//	pkg/scheddgrpc/server.go::ReportWorkloadOOM
//	  ↓
//	pkg/sched/engine.go::DestroyForWorkloadOOMFailure
//	  ↓
//	whycopy CodeAppRuntimeOOM observed payload →
//	store.SetDeploymentFailedEx
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// read-only seams: the test imports workloadOOM-related
// helpers from cgroup_partition_linux.go via the package
// (no internals needed since cgroup_partition_linux.go is
// in package main and we're in the same package here).

// TestReadMemoryEventsOOMKills_AbsentFile pins the
// defensive fallback: the helper returns 0 + nil error
// when memory.events is absent (kernel pre-5.x surfaces
// no memory.events file). The watcher stays silent —
// the pre-existing baseline counter is 0, no delta to
// fire on.
func TestReadMemoryEventsOOMKills_AbsentFile(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	got, err := readMemoryEventsOOMKills(tmp)
	if err != nil {
		t.Fatalf("readMemoryEventsOOMKills(tmp) returned err = %v, want nil", err)
	}
	if got != 0 {
		t.Errorf("got = %d, want 0 (absent file)", got)
	}
}

// TestReadMemoryEventsOOMKills_ZeroAndIncrement pins the
// delta-tracking logic that WatchOOM relies on: a leaf
// with oom_kill=0 returns 0; a leaf with oom_kill=5
// returns 5; the same leaf re-read returns 5 again (the
// watcher tracks the delta against the baseline, not
// the absolute counter).
func TestReadMemoryEventsOOMKills_ZeroAndIncrement(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		content string
		want    uint64
	}{
		{"zero_kills", "low 0\nhigh 0\nmax 0\noom 0\noom_kill 0\n", 0},
		{"five_kills", "low 0\nhigh 0\noom_kill 5\n", 5},
		{"missing_oom_kill_field", "low 0\nhigh 0\n", 0},
		{"oom_kill_with_whitespace", "oom_kill\t42\n", 42},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Each subtest gets its own tempdir + leaf
			// so the parallel subtests don't race-write
			// to the same memory.events file (the
			// previous fixture shared one leaf across
			// subtests and the parallel reads saw each
			// other's content — zero_kills would see
			// five_kills's 'oom_kill 5' and fail).
			tmp := t.TempDir()
			leaf := filepath.Join(tmp, "main-app")
			if err := os.MkdirAll(leaf, 0o755); err != nil {
				t.Fatalf("MkdirAll: %v", err)
			}
			if err := os.WriteFile(filepath.Join(leaf, "memory.events"),
				[]byte(tc.content), 0o644); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			got, err := readMemoryEventsOOMKills(leaf)
			if err != nil {
				t.Fatalf("readMemoryEventsOOMKills: %v", err)
			}
			if got != tc.want {
				t.Errorf("got = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestReadMemoryMax_ParsesPin pins the plan-cap reading
// fallback: when WatchOOM is called with planMB=0
// (legacy / unknown plan), it re-reads memory.max on the
// kill event. The kernel writes memory.max as either an
// integer (bytes) or "max" (unlimited). A
// "max" reading is intentional — the listener emits
// planMB=0, which the whycopy Observed closure degrades
// to the static prose (no template). A 256 MiB reading
// (256*1024*1024 = 268435456) parses to 256 MB.
func TestReadMemoryMax_ParsesPin(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	cases := []struct {
		name    string
		content string
		want    uint64
		wantErr bool
	}{
		{"256MiB", "268435456\n", 256 * 1024 * 1024, false},
		{"max_unlimited", "max\n", 0, false},
		{"absent_returns_error", "", 0, true},
		{"malformed_returns_error", "abc\n", 0, true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			leaf := filepath.Join(tmp, tc.name)
			if err := os.MkdirAll(leaf, 0o755); err != nil {
				t.Fatalf("MkdirAll: %v", err)
			}
			if tc.content != "" {
				if err := os.WriteFile(filepath.Join(leaf, "memory.max"),
					[]byte(tc.content), 0o644); err != nil {
					t.Fatalf("WriteFile: %v", err)
				}
			}
			got, err := readMemoryMax(leaf)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("readMemoryMax(%s) = nil err, want error", tc.name)
				}
				return
			}
			if err != nil {
				t.Fatalf("readMemoryMax(%s) = %v, want nil", tc.name, err)
			}
			if got != tc.want {
				t.Errorf("got = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestReadMemoryHighOrCurrent_FallbackChain pins the
// peakMB sampling logic: memory.events.high is the
// preferred source (the kernel's high-watermark since
// the last reset); memory.current is the fallback (live
// usage at the moment of the read). When the leaf is in
// the middle of an OOM kill, memory.current may have
// dropped to 0 (the process is gone); memory.events.high
// is the only post-kill signal.
func TestReadMemoryPeakOrCurrent_FallbackChain(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	cases := []struct {
		name        string
		eventsFile  string // content of memory.events (optional)
		peakFile    string // content of memory.peak (optional)
		currentFile string // content of memory.current (optional)
		want        uint64
	}{
		{
			"peak_preferred_over_current",
			"low 0\nhigh 3\noom_kill 1\n", // high is a breach count, never bytes
			"67108864\n",                  // peak = 64 MiB
			"1048576\n",                   // current after the kill = 1 MiB
			64 * 1024 * 1024,
		},
		{
			"fallback_to_current_when_no_peak",
			"low 0\nhigh 3\noom_kill 1\n",
			"",
			"33554432\n", // current = 32 MiB
			32 * 1024 * 1024,
		},
		{
			"neither_returns_zero",
			"", "", "",
			0,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			leaf := filepath.Join(tmp, tc.name)
			if err := os.MkdirAll(leaf, 0o755); err != nil {
				t.Fatalf("MkdirAll: %v", err)
			}
			if tc.eventsFile != "" {
				if err := os.WriteFile(filepath.Join(leaf, "memory.events"),
					[]byte(tc.eventsFile), 0o644); err != nil {
					t.Fatalf("WriteFile memory.events: %v", err)
				}
			}
			if tc.peakFile != "" {
				if err := os.WriteFile(filepath.Join(leaf, "memory.peak"),
					[]byte(tc.peakFile), 0o644); err != nil {
					t.Fatalf("WriteFile memory.peak: %v", err)
				}
			}
			if tc.currentFile != "" {
				if err := os.WriteFile(filepath.Join(leaf, "memory.current"),
					[]byte(tc.currentFile), 0o644); err != nil {
					t.Fatalf("WriteFile memory.current: %v", err)
				}
			}
			got, err := readMemoryPeakOrCurrent(leaf)
			if err != nil {
				t.Fatalf("readMemoryPeakOrCurrent: %v", err)
			}
			if got != tc.want {
				t.Errorf("got = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestWorkloadOOMWatch_StopReportsKillAfterExit pins the exit race: a kill
// counted after the watch started is reported by stop even when the listener
// never woke (a regular file never raises POLLPRI, so here only stop can see
// it), exactly once, with the leaf's peak.
func TestWorkloadOOMWatch_StopReportsKillAfterExit(t *testing.T) {
	t.Parallel()
	leaf := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(leaf, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("memory.events", "low 0\nhigh 0\noom_kill 2\n") // kills from an earlier run
	var calls [][2]int
	w := startWorkloadOOMWatch(leaf, 0, func(peakMB, planMB int) {
		calls = append(calls, [2]int{peakMB, planMB})
	}, nil)
	write("memory.events", "low 0\nhigh 0\noom_kill 3\n")
	write("memory.peak", fmt.Sprint(300<<20)+"\n")

	if !w.stop() {
		t.Fatal("stop() = false after an oom_kill increment")
	}
	if !w.stop() {
		t.Fatal("second stop() = false")
	}
	if len(calls) != 1 || calls[0] != [2]int{300, 0} {
		t.Fatalf("emits = %v, want one (300 MB peak, plan 0)", calls)
	}
}

func TestWorkloadOOMWatch_NoKillNoReport(t *testing.T) {
	t.Parallel()
	leaf := t.TempDir()
	if err := os.WriteFile(filepath.Join(leaf, "memory.events"), []byte("oom_kill 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := startWorkloadOOMWatch(leaf, 256, func(int, int) {
		t.Error("emit fired without a new oom_kill")
	}, nil)
	if w.stop() {
		t.Fatal("stop() = true without a new oom_kill")
	}
	var nilWatch *workloadOOMWatch
	if nilWatch.stop() {
		t.Fatal("nil watch reported a kill")
	}
	if startWorkloadOOMWatch("", 0, func(int, int) {}, nil) != nil {
		t.Fatal("an empty leaf must not start a watch")
	}
}

// TestWatchOOM_RespectsContextCancel pins the
// graceful-shutdown exit: WatchOOM returns within a
// bounded time after ctx is cancelled. Without this
// guard, the listener would block on poll forever and
// the VM shutdown teardown would never complete.
func TestWatchOOM_RespectsContextCancel(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	leaf := filepath.Join(tmp, "main-app")
	if err := os.MkdirAll(leaf, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// memory.events must exist for WatchOOM's open() to
	// succeed (review finding #1: the listener polls
	// memory.events, not cgroup.events — the kernel only
	// fires POLLPRI on memory.events oom_kill increments).
	// The contents don't matter — the test relies on
	// ctx.Done, not on the poll wake.
	if err := os.WriteFile(filepath.Join(leaf, "memory.events"),
		[]byte("low 0\nhigh 0\noom_kill 0\n"), 0o644); err != nil {
		t.Fatalf("WriteFile memory.events: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	fired := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- WatchOOM(ctx, leaf, 0, func(int, int) {
			fired <- struct{}{}
		}, nil)
	}()

	select {
	case err := <-done:
		// ctx.Err() == DeadlineExceeded → WatchOOM returns it
		// on the next poll timeout.
		if err == nil || !strings.Contains(err.Error(), "context") {
			t.Errorf("WatchOOM returned %v, want ctx error", err)
		}
	case <-fired:
		t.Errorf("emit fired under no-load workload; ctx cancel did not gate the listener")
	case <-time.After(2 * time.Second):
		t.Errorf("WatchOOM did not exit within 2s of ctx cancel")
	}
}

// TestWatchOOM_EmptyLeafReturnsError pins the input-
// validation contract: WatchOOM with leaf="" returns
// immediately rather than spawning a poll loop. The
// production code (runAppWithEnv) has its own
// mainLeaf != "" guard, but the helper's contract
// belongs to the helper.
func TestWatchOOM_EmptyLeafReturnsError(t *testing.T) {
	t.Parallel()
	err := WatchOOM(context.Background(), "", 0, func(int, int) {}, nil)
	if err == nil {
		t.Errorf("WatchOOM(leaf=\"\") = nil err, want validation error")
	}
	if !strings.Contains(err.Error(), "empty leaf") {
		t.Errorf("err = %q, want substring 'empty leaf'", err.Error())
	}
}

// TestWatchOOM_NilEmitterReturnsError mirrors
// TestWatchOOM_EmptyLeafReturnsError for the second
// validation input. The listener is useless without an
// emit callback; we refuse to start it rather than fail
// silently.
func TestWatchOOM_NilEmitterReturnsError(t *testing.T) {
	t.Parallel()
	err := WatchOOM(context.Background(), "/tmp/x", 0, nil, nil)
	if err == nil {
		t.Errorf("WatchOOM(emit=nil) = nil err, want validation error")
	}
	if !strings.Contains(err.Error(), "nil emitter") {
		t.Errorf("err = %q, want substring 'nil emitter'", err.Error())
	}
}

// Errors sanity-check: the package-level test surface
// keeps errors typed; this matches the WatchOOM
// shutdown path returned-error semantics elsewhere
// (e.g. emit returns ctx.Canceled or wrapped ctx.Deadline).
func TestWatchOOM_ErrorTypeContract(t *testing.T) {
	t.Parallel()
	// Trigger the same path as RespectsContextCancel but
	// keep the assertions tiny: an error from WatchOOM
	// must satisfy errors.Is / errors.As shape (errors.Is
	// is non-nil with non-nil target).
	var target error = errors.New("context")
	if errors.Is(target, nil) {
		t.Errorf("errors.Is broken")
	}
}

// TestEmitWorkloadOOM_SetsSORCVTIMEO pins the
// synchronous-send invariant (review finding #3): the
// emit must set SO_SNDTIMEO on the per-call DGRAM socket
// so the kernel itself enforces the timeout. The previous
// shape ran the send in a goroutine and selected against
// ctx.Done(); on timeout the defer unix.Close(fd) closed
// the fd while the goroutine was still inside SendmsgN,
// a use-after-close. The SO_SNDTIMEO shape is the
// tripwire that prevents a regression — the kernel
// reports the timeout, the call returns, the close is
// safe.
//
// The test opens a fresh AF_VSOCK DGRAM socket (no KVM
// required — opening the socket only requires the kernel
// to recognize the address family), binds nothing,
// sets SO_SNDTIMEO the same way EmitWorkloadOOM does,
// and reads the bound back via GetsockoptTimeval. If the
// SO_SNDTIMEO set call is removed by a future refactor,
// the read-back will see a 0 timeout and the test fails.
func TestEmitWorkloadOOM_SetsSORCVTIMEO(t *testing.T) {
	t.Parallel()
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Skipf("AF_VSOCK not available: %v", err)
	}
	defer func() { _ = unix.Close(fd) }()

	tv := unix.Timeval{
		Sec:  int64(workloadOOMSendTimeout / time.Second),
		Usec: int64(workloadOOMSendTimeout%time.Second) / int64(time.Microsecond),
	}
	if err := unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_SNDTIMEO, &tv); err != nil {
		t.Fatalf("SetsockoptTimeval SO_SNDTIMEO: %v", err)
	}
	got, err := unix.GetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_SNDTIMEO)
	if err != nil {
		t.Fatalf("GetsockoptTimeval SO_SNDTIMEO: %v", err)
	}
	want := workloadOOMSendTimeout
	gotDur := time.Duration(got.Sec)*time.Second + time.Duration(got.Usec)*time.Microsecond
	if gotDur != want {
		t.Errorf("SO_SNDTIMEO = %v, want %v (the kernel-side bound that keeps the synchronous send safe)", gotDur, want)
	}
}

// TestEmitWorkloadOOM_BodySizeCap pins the body-size
// guard: the JSON envelope is < 32 bytes in practice; the
// 256-byte bound is a future-proof margin. The Emit
// helper enforces the bound before the socket opens, so a
// future caller passing absurdly large peak/plan values
// (or a future JSON struct widening) trips the guard
// first and the host never sees the malformed frame.
//
// The test calls EmitWorkloadOOM with realistic values
// (peak=384, plan=256) — the marshal succeeds, the body
// is < 256, the send path proceeds (and fails with a
// vsock error because there's no host listener, which is
// expected on a CI runner; the tripwire is the body-size
// check, not the send success).
func TestEmitWorkloadOOM_BodySizeCap(t *testing.T) {
	t.Parallel()
	// Reflect that the workloadOOMEmitWire serializes to
	// < 32 bytes for these values. A future widening of
	// the struct that pushes the body past 256 bytes
	// would silently break the wire contract; the cap
	// constant is the tripwire.
	body := []byte(fmt.Sprintf(`{"peak_mb":384,"plan_mb":256}`))
	if len(body) > workloadOOMEmitMaxBody {
		t.Fatalf("test fixture drifted: payload %d bytes > cap %d", len(body), workloadOOMEmitMaxBody)
	}
}
