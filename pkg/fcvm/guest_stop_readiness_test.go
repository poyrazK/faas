package fcvm

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// spec: §6.1
// production-us hunt #8: a crash-looping container (nginx could not reopen
// /dev/stderr as uid 101) made guest-init exit and the guest kernel panic
// 1.5 s after boot, but readiness kept dialing the dead guest for its whole
// 2-minute deadline and then blamed the app for not listening. Readiness now
// stops when the instance's Firecracker process exits.
func TestCancelOnGuestStopCancelsWhenTheGuestExits(t *testing.T) {
	rec := &instanceRecord{done: make(chan struct{})}
	v := &JailerVMM{recs: map[string]*instanceRecord{"inst": rec}}
	ctx, stop := v.cancelOnGuestStop(context.Background(), "inst")
	defer stop()
	close(rec.done)
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("readiness context still live after the guest exited")
	}
	if !errors.Is(context.Cause(ctx), errGuestStopped) {
		t.Fatalf("cause = %v, want errGuestStopped", context.Cause(ctx))
	}
	if v.guestStopRequested("inst") {
		t.Fatal("an unrequested exit reported as an explicit stop")
	}
	rec.stopping = true
	if !v.guestStopRequested("inst") {
		t.Fatal("an explicit stop must keep its own error")
	}
}

// spec: §6.1
func TestCancelOnGuestStopLeavesUnknownInstancesAlone(t *testing.T) {
	v := &JailerVMM{}
	ctx, stop := v.cancelOnGuestStop(context.Background(), "missing")
	stop()
	if ctx.Err() != nil {
		t.Fatalf("context cancelled for an instance without a record: %v", ctx.Err())
	}
}

// spec: §6.1
// The guest-init exit ends in a kernel panic trace; the reported tail must
// keep the workload's own error, not the trace.
func TestWorkloadOutputTailDropsGuestKernelLines(t *testing.T) {
	lines := []string{
		"guest-init: stage pivot",
		`nginx: [alert] could not open error log file: open() "/var/log/nginx/error.log" failed (13: Permission denied)`,
		"guest-init: app crash-looped after 3 restart(s): run [/docker-entrypoint.sh nginx -g daemon off;]: exit status 1",
		"[    1.445781] Kernel panic - not syncing: Attempted to kill init! exitcode=0x00000100",
		"[    1.457475]  panic+0x102/0x27b",
		"",
	}
	got := workloadOutputTail(lines, 2)
	if strings.Contains(got, "Kernel panic") || strings.Contains(got, "panic+0x") {
		t.Fatalf("tail kept kernel lines: %q", got)
	}
	if !strings.Contains(got, "Permission denied") || !strings.Contains(got, "crash-looped") {
		t.Fatalf("tail lost the workload error: %q", got)
	}
	if strings.Contains(got, "stage pivot") {
		t.Fatalf("tail exceeded max lines: %q", got)
	}
}

// spec: §6.1
// A restored guest must not report RCU stalls into customer logs (hunt #8).
func TestGuestBootArgsSuppressRCUStallWarnings(t *testing.T) {
	for name, args := range map[string]string{"cold boot": coldBootArgs, "execution": executionBootArgs} {
		if !strings.Contains(args, "rcupdate.rcu_cpu_stall_suppress=1") {
			t.Errorf("%s boot args lack RCU stall suppression: %q", name, args)
		}
	}
}

// spec: §6.1
// A worker/job whose guest exits during startup loses the characterization
// race; the error must come from the guest's exit, not the receipt timeout.
func TestGuestExitedWithinDistinguishesUnrequestedExits(t *testing.T) {
	rec := &instanceRecord{done: make(chan struct{})}
	v := &JailerVMM{recs: map[string]*instanceRecord{"inst": rec}}
	if v.guestExitedWithin("inst", 0) {
		t.Fatal("a running guest reported as exited")
	}
	go func() {
		time.Sleep(20 * time.Millisecond)
		close(rec.done)
	}()
	if !v.guestExitedWithin("inst", time.Second) {
		t.Fatal("an exit inside the grace window was missed")
	}
	rec.stopping = true
	if v.guestExitedWithin("inst", 0) {
		t.Fatal("an explicit stop must keep its own error")
	}
	if v.guestExitedWithin("missing", 0) {
		t.Fatal("an unknown instance reported as exited")
	}
}
