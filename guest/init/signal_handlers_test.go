//go:build linux

package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// TestParseStopSignal_DefaultsToSIGTERM pins the empty-string
// fall-back: a manifest with no StopSignal declared must still
// resolve to SIGTERM (the OCI image-config default). This is the
// contract every existing deployment relies on — a regression
// would break graceful-stop for every customer who hasn't set
// STOPSIGNAL explicitly.
func TestParseStopSignal_DefaultsToSIGTERM(t *testing.T) {
	if got := parseStopSignal(""); got != syscall.SIGTERM {
		t.Errorf("parseStopSignal(\"\") = %v; want SIGTERM", got)
	}
}

// TestParseStopSignal_AcceptsOCINames covers the OCI image-config
// STOPSIGNAL vocabulary. The OCI spec allows "SIG<name>" or bare
// "<name>"; both are honoured. Unknown / malformed values fall
// back to SIGTERM (fail-open — better to stop the customer than
// fail closed on a typo).
func TestParseStopSignal_AcceptsOCINames(t *testing.T) {
	cases := []struct {
		in   string
		want syscall.Signal
	}{
		{"SIGTERM", syscall.SIGTERM},
		{"TERM", syscall.SIGTERM},
		{"SIGUSR1", syscall.SIGUSR1},
		{"USR1", syscall.SIGUSR1},
		{"sigusr1", syscall.SIGUSR1},
		{"  SIGUSR2  ", syscall.SIGUSR2},
		{"SIGHUP", syscall.SIGHUP},
		{"SIGINT", syscall.SIGINT},
		{"BOGUS_SIGNAL_THAT_DOES_NOT_EXIST", syscall.SIGTERM},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := parseStopSignal(tc.in); got != tc.want {
				t.Errorf("parseStopSignal(%q) = %v; want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestStopSignalFromManifest_MirrorsParseStopSignal pins the
// exported wrapper to the same shape as the internal helper.
// The wrapper exists only so tests outside guest/init can drive
// the parser via the exported surface; if the two diverge, the
// production boot path and the test rig would see different
// behaviours.
func TestStopSignalFromManifest_MirrorsParseStopSignal(t *testing.T) {
	if StopSignalFromManifest("SIGUSR1") != syscall.SIGUSR1 {
		t.Errorf("wrapper returned wrong value for SIGUSR1")
	}
	if StopSignalFromManifest("") != syscall.SIGTERM {
		t.Errorf("wrapper returned wrong default for empty")
	}
}

// spawnStopPy3 starts python3 with the given script. Skips the
// test if python3 is unavailable (matches the portable fcvm
// helper at pkg/fcvm/vmm_signal_kill_test.go).
func spawnStopPy3(t *testing.T, script string) *exec.Cmd {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skipf("python3 not available: %v", err)
	}
	cmd := exec.Command("python3", "-c", script)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start python3: %v", err)
	}
	return cmd
}

// TestSupervisor_Stop_ChildHonoursSIGTERM pins the graceful-stop
// happy path: a child that registers a SIGTERM handler and exits
// cleanly within the grace window. Stop returns nil and the
// supervisor's lastCmd's Wait completes. Mirrors
// TestSignalAndKillRace_CleanExit (pkg/fcvm) at the guest-init
// layer.
func TestSupervisor_Stop_ChildHonoursSIGTERM(t *testing.T) {
	cmd := spawnStopPy3(t, `
import signal, os, time
def h(s, f): os._exit(0)
signal.signal(signal.SIGTERM, h)
time.sleep(60)`)
	time.Sleep(readyDelayStop)

	sup := &Supervisor{Max: MaxRestarts}
	sup.TrackCommand(cmd)
	defer stopWatchHelper(t, cmd)

	start := time.Now()
	err := sup.Stop(context.Background(), syscall.SIGTERM, 3*time.Second)
	elapsed := time.Since(start)
	if err != nil {
		t.Errorf("Stop returned %v; want nil (clean exit)", err)
	}
	if elapsed > 1*time.Second {
		t.Errorf("Stop took %v; want < 1s (clean exit)", elapsed)
	}
}

// TestSupervisor_Stop_GraceExpiresEscalatesToSIGKILL pins the
// escalation path: a child that ignores SIGTERM causes the
// grace timer to fire; Stop escalates to SIGKILL. The test
// asserts the post-SIGKILL wait completes within the post-
// kill bound (grace + slack).
func TestSupervisor_Stop_GraceExpiresEscalatesToSIGKILL(t *testing.T) {
	cmd := spawnStopPy3(t, `
import signal, time
signal.signal(signal.SIGTERM, signal.SIG_IGN)
time.sleep(60)`)
	time.Sleep(readyDelayStop)

	sup := &Supervisor{Max: MaxRestarts}
	sup.TrackCommand(cmd)
	defer stopWatchHelper(t, cmd)

	grace := 500 * time.Millisecond
	start := time.Now()
	err := sup.Stop(context.Background(), syscall.SIGTERM, grace)
	elapsed := time.Since(start)
	if err != nil {
		t.Errorf("Stop returned %v; want nil (escalation succeeds)", err)
	}
	if elapsed < grace {
		t.Errorf("escalated too fast: %v; want >= grace=%v", elapsed, grace)
	}
	if elapsed > grace+grace+500*time.Millisecond {
		t.Errorf("escalated too slow: %v; want <= grace+post-kill bound", elapsed)
	}
}

// TestSupervisor_Stop_NoForkIsNoOp pins the no-op-on-empty case:
// a supervisor that never had a fork recorded (LastAppPID=-1)
// returns nil silently from Stop. This is the steady state for
// a Start closure that fails before cmd.Start() (e.g. a bad
// argv) — the engine's StopInstance must not panic or block.
func TestSupervisor_Stop_NoForkIsNoOp(t *testing.T) {
	sup := &Supervisor{Max: MaxRestarts}
	err := sup.Stop(context.Background(), syscall.SIGTERM, time.Second)
	if err != nil {
		t.Errorf("Stop returned %v; want nil (no fork)", err)
	}
}

// TestSupervisor_ForwardSignal_SendsUSR1ToChild pins the signal
// forwarding contract: STOPSIGNAL=SIGUSR1 must deliver SIGUSR1
// to the tracked workload (the customer's PID 1). Python's
// signal.signal(SIGUSR1, h) catches the delivery and exits 42;
// we assert via Wait().
func TestSupervisor_ForwardSignal_SendsUSR1ToChild(t *testing.T) {
	cmd := spawnStopPy3(t, `
import signal, os, time
def h(s, f): os._exit(42)
signal.signal(signal.SIGUSR1, h)
time.sleep(60)`)
	time.Sleep(readyDelayStop)

	sup := &Supervisor{Max: MaxRestarts}
	sup.TrackCommand(cmd)
	defer stopWatchHelper(t, cmd)

	if err := sup.ForwardSignal(syscall.SIGUSR1); err != nil {
		t.Fatalf("ForwardSignal: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			if ee.ExitCode() != 42 {
				t.Errorf("child exit = %d; want 42 (SIGUSR1 handler)", ee.ExitCode())
			}
			return
		}
		t.Fatalf("Wait: %v", err)
	}
	t.Errorf("child exited cleanly; want exit=42")
}

// TestSupervisor_ForwardSignal_AlreadyExitedIsBenign pins the
// post-stop tail: forwarding to a child that's already gone
// returns os.ErrProcessDone (the kernel's ESRCH surface) so
// the caller's warn-log doesn't spam on every post-stop tick.
func TestSupervisor_ForwardSignal_AlreadyExitedIsBenign(t *testing.T) {
	cmd := spawnStopPy3(t, `import sys; sys.exit(0)`)
	time.Sleep(readyDelayStop)
	// Wait for the child to exit and the kernel to release its PID.
	_ = cmd.Wait()

	sup := &Supervisor{Max: MaxRestarts}
	sup.TrackCommand(cmd)
	err := sup.ForwardSignal(syscall.SIGUSR1)
	if !errors.Is(err, os.ErrProcessDone) {
		t.Errorf("ForwardSignal returned %v; want os.ErrProcessDone", err)
	}
}

// TestSupervisor_Stop_IdempotentOnSecondCall pins the
// sync.Once guard: a second Stop() call must not re-send
// SIGTERM or re-start the grace timer. Without this guard, a
// manifest that declared two stop-signals could trigger two
// parallel stop sequences racing each other.
func TestSupervisor_Stop_IdempotentOnSecondCall(t *testing.T) {
	cmd := spawnStopPy3(t, `
import signal, os, time
counter = [0]
def h(s, f):
    counter[0] += 1
    os._exit(0)
signal.signal(signal.SIGTERM, h)
time.sleep(60)`)
	time.Sleep(readyDelayStop)

	sup := &Supervisor{Max: MaxRestarts}
	sup.TrackCommand(cmd)
	defer stopWatchHelper(t, cmd)

	// First Stop: triggers the signal.
	if err := sup.Stop(context.Background(), syscall.SIGTERM, time.Second); err != nil {
		t.Fatalf("first Stop: %v", err)
	}
	// Second Stop: must be a no-op (sync.Once). The first
	// call already sent SIGTERM; a second send would fail
	// with ESRCH (process already gone). If sync.Once is
	// broken, the second call attempts the send and
	// surfaces an error.
	if err := sup.Stop(context.Background(), syscall.SIGTERM, time.Second); err != nil {
		t.Errorf("second Stop returned %v; want nil (idempotent)", err)
	}
}

// readyDelayStop gives the spawned child time to set up its
// signal handlers before the test sends a signal. Without this
// delay, the first signal lands before signal.signal() is
// registered and the kernel's default SIGTERM action
// terminates the child before our test can observe it. 100ms
// matches the portable fcvm test rig.
const readyDelayStop = 100 * time.Millisecond

// stopWatchHelper drains cmd.Wait() so the test's spawned
// python process doesn't leak into the test runner's process
// tree. Safe to call multiple times (Wait returns
// os.ErrProcessDone on the second call).
func stopWatchHelper(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	_ = cmd.Wait()
}
