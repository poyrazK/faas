package fcvm

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
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
// production-us hunt #9 (H9-91): a Node app that threw at startup failed its
// deploy with "workload stopped during startup (guest … exited before
// becoming ready)" and no output: the ring was read with Snapshot(0), which
// means "tail from now". The error must carry the workload's own last lines.
func TestGuestStoppedDuringStartupReportsTheWorkloadOutput(t *testing.T) {
	v := NewJailerVMM(t.TempDir(), 0)
	ring := v.registerRing("inst")
	for _, line := range []string{
		"booting broken\n",
		"Error: boom at startup\n",
		"[    0.912345] Kernel panic - not syncing: Attempted to kill init! exitcode=0x00000100\n",
	} {
		if _, err := ring.Write("stdout", []byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	err := v.guestStoppedDuringStartup(Lease{Instance: "inst"})
	if err == nil || !strings.Contains(err.Error(), "boom at startup") || !strings.Contains(err.Error(), "booting broken") {
		t.Fatalf("startup failure = %v, want the workload's output", err)
	}
	if strings.Contains(err.Error(), "Kernel panic") {
		t.Fatalf("startup failure kept kernel lines: %v", err)
	}
}

// spec: §6.1, §12
// production-us hunt #9 (H9-106): a customer app that crashed at startup
// failed its deploy as error_code "internal", which the public status page
// counts as a platform deploy failure. guest-init's supervisor lines prove
// the customer's workload ran and exited; a guest that died without them
// (overlay, pivot, init failure) stays a platform error.
func TestGuestStoppedDuringStartupAttributesSupervisedCrashesToTheApp(t *testing.T) {
	for _, tc := range []struct {
		name      string
		lines     []string
		wantCrash bool
	}{
		{name: "crash loop", lines: []string{
			"Error: Cannot find module 'express'\n",
			"guest-init: app restart (restart 1/3 policy=on-failure): exit status 1\n",
			"guest-init: app crash-looped after 3 restart(s): exit status 1\n",
		}, wantCrash: true},
		{name: "workload set main", lines: []string{
			"guest-init: main restart (restart 1/3 policy=on-failure): exit status 2\n",
		}, wantCrash: true},
		{name: "restart budget", lines: []string{
			"guest-init: app restart budget exhausted after 3 restart(s)\n",
		}, wantCrash: true},
		{name: "guest-init failure", lines: []string{
			"guest-init: assemble overlay: mount overlay: invalid argument\n",
			"[    0.912345] Kernel panic - not syncing: Attempted to kill init! exitcode=0x00000100\n",
		}},
		{name: "no output"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := NewJailerVMM(t.TempDir(), 0)
			ring := v.registerRing("inst")
			for _, line := range tc.lines {
				if _, err := ring.Write("stdout", []byte(line)); err != nil {
					t.Fatal(err)
				}
			}
			err := v.guestStoppedDuringStartup(Lease{Instance: "inst"})
			p := api.AsProblem(err)
			if tc.wantCrash {
				if p == nil || p.Code != api.CodeAppStartupCrash || p.Status != 422 || !strings.Contains(p.Detail, "exited before becoming ready") {
					t.Fatalf("startup failure = %#v, want a 422 %s problem carrying the detail", err, api.CodeAppStartupCrash)
				}
				return
			}
			if p != nil {
				t.Fatalf("guest failure without supervisor output = %#v, want a platform (non-problem) error", p)
			}
		})
	}
}

// spec: §6.1, §12
// A characterization report of a startup exit is the workload's own exit code.
func TestCharacterizationStartupExitIsAnAppStartupCrash(t *testing.T) {
	terminal, err := characterizationBootOutcome(api.CharacterizationReport{ExitCode: 17, LogTail: "boom"}, api.ExecutionModeRequest)
	p := api.AsProblem(err)
	if !terminal || p == nil || p.Code != api.CodeAppStartupCrash || !strings.Contains(p.Detail, "code 17") || !strings.Contains(p.Detail, "boom") {
		t.Fatalf("startup exit = (%v, %#v), want terminal %s with exit code and tail", terminal, err, api.CodeAppStartupCrash)
	}
}

// spec: §6.1, §12
// A workload that contradicts its declared mode is the customer's to fix, so it
// carries a stable customer-caused code instead of "internal" (H10-135).
func TestExecutionModeConflictIsCustomerCaused(t *testing.T) {
	tests := []struct {
		name   string
		mode   string
		report api.CharacterizationReport
	}{
		{"worker bound a port", api.ExecutionModeWorker, api.CharacterizationReport{ObservedClass: "http", ObservedPort: 8080, ExitCode: -1}},
		{"worker exited cleanly", api.ExecutionModeWorker, api.CharacterizationReport{ObservedClass: "job", ExitCode: 0}},
		{"job served", api.ExecutionModeJob, api.CharacterizationReport{ObservedClass: "grpc", ObservedPort: 50051, ExitCode: -1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			terminal, err := characterizationBootOutcome(tt.report, tt.mode)
			p := api.AsProblem(err)
			if !terminal || p == nil || p.Code != api.CodeExecutionModeConflict || p.Status != 422 || !strings.Contains(p.Detail, tt.report.ObservedClass) {
				t.Fatalf("conflict = (%v, %#v), want terminal 422 %s naming %q", terminal, err, api.CodeExecutionModeConflict, tt.report.ObservedClass)
			}
		})
	}
	if !slices.Contains(api.CustomerCausedDeploymentFailureCodes(), api.CodeExecutionModeConflict) {
		t.Fatalf("%s is not customer-caused; the status page would count it as a platform failure", api.CodeExecutionModeConflict)
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
