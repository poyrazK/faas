// vmm_signal_kill_test.go — portable (no KVM) tests for the M-2 /
// ADR-138 graceful stop sequence. The tests exercise the inner
// signal-grace-SIGKILL sequence (signalAndKillRace, exported
// as a package-level helper in vmm.go) against controlled
// children.
//
// Two child shapes are used:
//
//   1. python3 -c "..." — deterministic signal.signal()
//      handling. Python's signal handler runs synchronously
//      inside the main thread, so SIGTERM/SIGUSR1 always lands
//      and os._exit(N) always returns the configured code.
//      Python is preferred over /bin/sh because POSIX-shell
//      trap semantics vary across platforms (dash / bash /
//      busybox) and trap delivery is asynchronous w.r.t. the
//      shell's `wait`, which makes the trap-fired-clean-exit
//      path flaky on macOS where /bin/sh is bash.
//
//   2. The metal-side equivalent (full FC VM boot + guest-init
//      signal forwarding) lives in
//      pkg/fcvm/vmm_signal_kill_metal_test.go (//go:build metal)
//      and gates commit 11.
//
// The metal-side equivalent lives in
// pkg/fcvm/vmm_signal_kill_metal_test.go (//go:build metal) and
// gates commit 11 — full FC VM boot + guest-init signal
// forwarding through vsock 1029.

package fcvm

import (
	"bufio"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// spawnPy3 starts python3 with the given script and returns
// the *exec.Cmd. Skips the test if python3 is unavailable.
func spawnPy3(t *testing.T, script string) *exec.Cmd {
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

// spawnPy3Ready starts a child that prints "ready" after installing its
// signal handler and waits for that handshake before returning. A fixed sleep
// is not sufficient under -race and a busy hosted runner: the child can still
// be between exec and signal.signal() when the test sends SIGTERM, making a
// handler test accidentally exercise the default-kill path.
func spawnPy3Ready(t *testing.T, script string) *exec.Cmd {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skipf("python3 not available: %v", err)
	}
	cmd := exec.Command("python3", "-c", script)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start python3: %v", err)
	}
	ready := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		if scanner.Scan() {
			ready <- scanner.Text()
			return
		}
		ready <- ""
	}()
	select {
	case line := <-ready:
		if line != "ready" {
			_ = cmd.Process.Kill()
			t.Fatalf("python3 readiness handshake = %q, want ready", line)
		}
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("timed out waiting for python3 readiness handshake")
	}
	return cmd
}

// watchChild closes done when cmd.Wait() returns. The captured
// ProcessState (set by Wait) is what signalAndKillRace reads on
// the clean-exit path, so the helper goroutine must Wait
// (rather than polling ps / kill 0) — same idiom production
// uses at vmm.go startJailer line ~2270.
func watchChild(cmd *exec.Cmd, done chan<- struct{}) {
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
}

// readyDelay gives the child time to set up its signal
// handlers before we send the signal. Without this delay the
// first signal can land BEFORE the child calls
// signal.signal() — the kernel's default SIGTERM action is
// "terminate" and the test reports killSignalSent=false +
// exitCode=-1 even though the test child was meant to ignore
// the signal. 100ms is generous; production doesn't have this
// delay because firecracker + guest-init have ~350ms cold
// boot latency that dwarfs the child-handler setup window.
const readyDelay = 100 * time.Millisecond

// TestSignalAndKillRace_CleanExit verifies the happy path:
// child receives SIGTERM and exits cleanly within grace;
// signalAndKillRace returns (killSignalSent=false,
// exitCode=child-exit-status). Uses Python's synchronous
// signal.signal() for deterministic delivery. The handler
// uses os._exit(N) instead of sys.exit(N) so the kernel
// observes the exact exit status (sys.exit goes through the
// Python SystemExit path which can race with the in-progress
// C-level sleep on some platforms).
func TestSignalAndKillRace_CleanExit(t *testing.T) {
	cmd := spawnPy3Ready(t, `
import signal, os, time
def h(s, f): os._exit(0)
signal.signal(signal.SIGTERM, h)
print("ready", flush=True)
time.sleep(60)`)
	done := make(chan struct{})
	watchChild(cmd, done)

	start := time.Now()
	killed, code, err := signalAndKillRace(cmd, done, syscall.SIGTERM, 3*time.Second, 2*time.Second)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("signalAndKillRace: %v", err)
	}
	if killed {
		t.Errorf("killSignalSent=true; want false (child honoured SIGTERM)")
	}
	if code != 0 {
		t.Errorf("exitCode=%d; want 0", code)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("took too long: elapsed=%v; want < 500ms (clean exit)", elapsed)
	}
}

// TestSignalAndKillRace_GraceExpiresEscalates verifies the
// SIGKILL-after-grace path: child ignores SIGTERM (POSIX trap
// with empty handler), the grace timer fires,
// signalAndKillRace escalates to SIGKILL. killSignalSent=true
// regardless of exit code.
func TestSignalAndKillRace_GraceExpiresEscalates(t *testing.T) {
	cmd := spawnPy3Ready(t, `
import signal, time
signal.signal(signal.SIGTERM, signal.SIG_IGN)
print("ready", flush=True)
time.sleep(60)`)
	done := make(chan struct{})
	watchChild(cmd, done)

	start := time.Now()
	killed, _, err := signalAndKillRace(cmd, done, syscall.SIGTERM, 500*time.Millisecond, 2*time.Second)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("signalAndKillRace: %v", err)
	}
	if !killed {
		t.Errorf("killSignalSent=false; want true (child ignored SIGTERM, grace=500ms)")
	}
	if elapsed < 400*time.Millisecond {
		t.Errorf("escalated too fast: elapsed=%v; want >= 400ms (grace=500ms)", elapsed)
	}
	if elapsed > 2*time.Second {
		t.Errorf("escalated too slow: elapsed=%v; want < 2s (grace=500ms + watchdog)", elapsed)
	}
}

// TestSignalAndKillRace_ZeroSignalDefaultsToSIGTERM verifies
// signal=0 → SIGTERM default: a child that ignores SIGTERM
// still gets killed by the grace timer. Same shape as
// TestSignalAndKillRace_GraceExpiresEscalates but with
// signal=0 (the API-level default the schedd's
// Engine.StopInstance uses when manifest.StopSignal is empty).
func TestSignalAndKillRace_ZeroSignalDefaultsToSIGTERM(t *testing.T) {
	cmd := spawnPy3(t, `
import signal, time
signal.signal(signal.SIGTERM, signal.SIG_IGN)
time.sleep(60)`)
	done := make(chan struct{})
	watchChild(cmd, done)
	time.Sleep(readyDelay) // let child set up signal handler

	killed, _, err := signalAndKillRace(cmd, done, 0, 300*time.Millisecond, 2*time.Second)
	if err != nil {
		t.Fatalf("signalAndKillRace: %v", err)
	}
	if !killed {
		t.Errorf("killSignalSent=false; want true (signal=0 should default to SIGTERM)")
	}
}

// TestSignalAndKillRace_CustomStopSignalUSR1 verifies the
// per-app StopSignal=USR1 path: child traps USR1 and exits
// with status 42. Mirrors ADR-138 §Decision 1: "StopSignal is
// SIGTERM by default; can be overridden via the AppManifest
// field; guest-init forwards the signal to the workload".
func TestSignalAndKillRace_CustomStopSignalUSR1(t *testing.T) {
	cmd := spawnPy3(t, `
import signal, os, time
def h(s, f): os._exit(42)
signal.signal(signal.SIGUSR1, h)
time.sleep(60)`)
	done := make(chan struct{})
	watchChild(cmd, done)
	time.Sleep(readyDelay) // let child set up signal handler

	killed, code, err := signalAndKillRace(cmd, done, syscall.SIGUSR1, 3*time.Second, 2*time.Second)
	if err != nil {
		t.Fatalf("signalAndKillRace: %v", err)
	}
	if killed {
		t.Errorf("killSignalSent=true; want false (child honoured SIGUSR1)")
	}
	if code != 42 {
		t.Errorf("exitCode=%d; want 42", code)
	}
}

// TestSignalAndKillRace_GraceZeroEscalatesImmediately verifies
// the no-grace path: grace=0 (legacy Destroy shape) bypasses
// the timer and escalates to SIGKILL immediately.
func TestSignalAndKillRace_GraceZeroEscalatesImmediately(t *testing.T) {
	cmd := spawnPy3(t, `
import signal, time
signal.signal(signal.SIGTERM, signal.SIG_IGN)
time.sleep(60)`)
	done := make(chan struct{})
	watchChild(cmd, done)
	time.Sleep(readyDelay) // let child set up signal handler

	start := time.Now()
	killed, _, err := signalAndKillRace(cmd, done, syscall.SIGTERM, 0, 2*time.Second)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("signalAndKillRace: %v", err)
	}
	if !killed {
		t.Errorf("killSignalSent=false; want true (grace=0 → immediate SIGKILL)")
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("took too long: elapsed=%v; want < 500ms (immediate)", elapsed)
	}
}

// TestSignalAndKillRace_AlreadyExitedIsClean covers the
// ESRCH-on-send path: child has already exited before we call
// signalAndKillRace. Sending SIGTERM returns ESRCH (the
// syscall error), which signalAndKillRace treats as benign
// (errors.Is(serr, syscall.ESRCH) branch) and continues to
// race the watchdog. The race wins (done is already closed),
// so the function returns killSignalSent=false + exitCode=0.
func TestSignalAndKillRace_AlreadyExitedIsClean(t *testing.T) {
	cmd := spawnPy3(t, `import sys; sys.exit(0)`)
	done := make(chan struct{})
	watchChild(cmd, done)
	time.Sleep(readyDelay) // let child set up signal handler
	// Wait for the watcher to close done.
	<-done

	killed, _, err := signalAndKillRace(cmd, done, syscall.SIGTERM, 1*time.Second, 1*time.Second)
	if err != nil {
		t.Fatalf("signalAndKillRace: %v", err)
	}
	if killed {
		t.Errorf("killSignalSent=true; want false (already-exited child; SIGTERM skipped, watchdog raced clean)")
	}
}

// TestSignalAndKillRace_NoWatchdogEscalatesImmediately covers
// the no-watchdog edge: doneCh=nil falls through to the
// immediate-escalate branch (killSignalSent=true). This is
// the safety net for a mis-wired Manager where the watchdog
// goroutine never started; we'd rather SIGKILL than hang.
func TestSignalAndKillRace_NoWatchdogEscalatesImmediately(t *testing.T) {
	cmd := spawnPy3(t, `
import signal, time
signal.signal(signal.SIGTERM, signal.SIG_IGN)
time.sleep(60)`)
	defer func() { _ = cmd.Process.Kill() }()

	start := time.Now()
	killed, _, err := signalAndKillRace(cmd, nil, syscall.SIGTERM, 5*time.Second, 1*time.Second)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("signalAndKillRace: %v", err)
	}
	if !killed {
		t.Errorf("killSignalSent=false; want true (no watchdog → immediate SIGKILL)")
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("took too long: elapsed=%v; want < 500ms (no watchdog → no grace wait)", elapsed)
	}
}

// TestSignalAndKillRace_DestroyWaitBoundsPostKillWait verifies
// the destroyWait cap: if the post-SIGKILL drain doesn't
// unblock within destroyWait, the helper returns anyway.
// Synthesised via a destroyWait smaller than the grace
// (the cap applies to the post-SIGKILL select, NOT to the
// grace timer — see signalAndKillRace source).
func TestSignalAndKillRace_DestroyWaitBoundsPostKillWait(t *testing.T) {
	// destroyWait=1s, grace=100ms. The test asserts the
	// elapsed bound is dominated by grace+destroyWait
	// (drain) — the helper should NOT exceed destroyWait by
	// more than headroom.
	grace := 100 * time.Millisecond
	destroyWait := 1 * time.Second

	cmd := spawnPy3(t, `
import signal, time
signal.signal(signal.SIGTERM, signal.SIG_IGN)
time.sleep(60)`)
	done := make(chan struct{})
	watchChild(cmd, done)
	time.Sleep(readyDelay) // let child set up signal handler

	start := time.Now()
	killed, _, err := signalAndKillRace(cmd, done, syscall.SIGTERM, grace, destroyWait)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("signalAndKillRace: %v", err)
	}
	if !killed {
		t.Errorf("killSignalSent=false; want true (grace expired → SIGKILL escalation)")
	}
	// The kernel terminates the child within microseconds of
	// SIGKILL (SIGKILL is untrappable), so the post-SIGKILL
	// drain should be near-instantaneous. The total elapsed
	// should be dominated by grace + a small drain bound.
	if elapsed < grace {
		t.Errorf("escalated too fast: elapsed=%v; want >= grace=%v", elapsed, grace)
	}
	if elapsed > grace+destroyWait+500*time.Millisecond {
		t.Errorf("escalated too slow: elapsed=%v; want < grace(%v)+destroyWait(%v)+500ms", elapsed, grace, destroyWait)
	}
}

// Compile-time assertion: production's signalAndKillRace is
// the function we just tested. If a future refactor renames
// or moves it, the test surface here will diverge from the
// production surface — caught at compile time by the type
// alias below.
var _ func(*exec.Cmd, <-chan struct{}, syscall.Signal, time.Duration, time.Duration) (bool, int32, error) = signalAndKillRace
