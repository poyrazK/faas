package fcvm

// Hunt 34 H34-35: a guest that stopped during boot surfaced as "apply current
// app CPU policy: … connection refused", hiding that Firecracker had exited.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var errRefusedVsockDial = errors.New("vmm: app CPU policy dial vsock uds /jail/vsock.sock: connect: connection refused")

func wakeWithFailingCPUPolicy(t *testing.T, onCPULimit func(*Manager, Lease)) error {
	t.Helper()
	return wakeWithCPUPolicyStep(t, errRefusedVsockDial, nil, onCPULimit)
}

// guestStopTestVMM explains a guest stop the way JailerVMM does from its
// console: with the workload's startup-crash problem.
type guestStopTestVMM struct {
	*cpuPolicyTestVMM
	explanation error
	explained   int
}

func (v *guestStopTestVMM) ExplainGuestStop(Lease) error {
	v.explained++
	return v.explanation
}

var errCompanionCrashLooped = api.NewProblem(422, api.CodeAppStartupCrash, "app exited during startup",
	"workload stopped during startup: Too many arguments!\nguest-init: sidecar echo crashed (restart 1/3): run sidecar echo: exit status 127")

// wakeWithCPUPolicyStep wakes an instance whose CPU policy step answers
// cpuLimitErr. A non-nil stop wraps the VMM so it explains guest stops.
func wakeWithCPUPolicyStep(t *testing.T, cpuLimitErr error, stop *guestStopTestVMM, onCPULimit func(*Manager, Lease)) error {
	t.Helper()
	withFakeCgroupRoot(t)
	vmm := &cpuPolicyTestVMM{fakeVMM: &fakeVMM{}, cpuLimitErr: cpuLimitErr}
	var managed VMM = vmm
	if stop != nil {
		stop.cpuPolicyTestVMM = vmm
		managed = stop
	}
	manager := newTestManager(&fakeRunner{}, managed)
	if onCPULimit != nil {
		vmm.onCPULimit = func(l Lease) { onCPULimit(manager, l) }
	}
	if err := manager.UpdateAppCPULimit(context.Background(), "app-1", 1, 250); err != nil {
		t.Fatalf("seed desired app CPU policy: %v", err)
	}
	_, err := manager.Wake(context.Background(), WakeRequest{
		Instance: "wake-exit", AppID: "app-1", BaseKey: "/base.ext4", LayerKey: "/layer.ext4",
		VcpuCount: 2, MemSizeMiB: 128, CPUMillicores: 1000, Plan: api.PlanPro,
	})
	if err == nil {
		t.Fatal("Wake succeeded with a failing CPU policy step")
	}
	return err
}

func TestWakeStepErrorNamesAFirecrackerExit(t *testing.T) {
	cases := []struct {
		name string
		exit func(*Manager, Lease)
	}{
		{"exit already reported", func(m *Manager, l Lease) { m.ProcessExited(l.Instance, 137) }},
		{"exit reported after the dial failed", func(m *Manager, l Lease) {
			go func() {
				time.Sleep(50 * time.Millisecond)
				m.ProcessExited(l.Instance, 137)
			}()
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := wakeWithFailingCPUPolicy(t, tc.exit)
			msg := err.Error()
			if !strings.Contains(msg, "firecracker exited during boot (exit code 137)") {
				t.Fatalf("error does not lead with the exit: %v", err)
			}
			if !errors.Is(err, errRefusedVsockDial) || !strings.Contains(msg, "apply current app CPU policy then failed") {
				t.Fatalf("error lost the failed step: %v", err)
			}
		})
	}
}

func TestWakeStepErrorWithoutAnExitKeepsTheStep(t *testing.T) {
	grace := wakeStepExitGrace
	wakeStepExitGrace = 60 * time.Millisecond
	t.Cleanup(func() { wakeStepExitGrace = grace })

	start := time.Now()
	err := wakeWithFailingCPUPolicy(t, nil)
	if strings.Contains(err.Error(), "firecracker exited") {
		t.Fatalf("named an exit that did not happen: %v", err)
	}
	if !errors.Is(err, errRefusedVsockDial) || !strings.Contains(err.Error(), "apply current app CPU policy: ") {
		t.Fatalf("error = %v, want the CPU policy step and its cause", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("waited %v for an exit; the grace is %v", elapsed, wakeStepExitGrace)
	}
}

// Hunt 34 H34-35: guest-init exited because an essential companion
// crash-looped, just after the main workload passed readiness. The guest's
// own output is the deploy's error, a customer startup crash, whichever wake
// step finds the exit; the step that tripped on it stays in the chain.
func TestWakeExplainsAGuestThatStoppedItself(t *testing.T) {
	exitClean := func(m *Manager, l Lease) { m.ProcessExited(l.Instance, 0) }
	cases := []struct {
		name        string
		cpuLimitErr error
		wantMessage string
	}{
		{"a later step fails on the dead VM", errRefusedVsockDial, "firecracker exited during boot (exit code 0)"},
		{"publication finds the exit", nil, "firecracker exited before lifecycle registration (exit code 0)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stop := &guestStopTestVMM{explanation: errCompanionCrashLooped}
			err := wakeWithCPUPolicyStep(t, tc.cpuLimitErr, stop, exitClean)
			if err == nil {
				t.Fatal("Wake succeeded after the guest stopped")
			}
			p := api.AsProblem(err)
			if p == nil || p.Code != api.CodeAppStartupCrash || !strings.Contains(p.Detail, "Too many arguments!") {
				t.Fatalf("error = %v, want the companion's startup crash", err)
			}
			if !strings.Contains(err.Error(), tc.wantMessage) {
				t.Fatalf("error = %v, want %q kept for operators", err, tc.wantMessage)
			}
			if tc.cpuLimitErr != nil && !errors.Is(err, tc.cpuLimitErr) {
				t.Fatalf("error lost the failed step: %v", err)
			}
		})
	}
}

// A Firecracker killed by a signal or failing on its own did not stop because
// of the guest; earlier restarts in the guest's output must not turn that
// into a customer crash.
func TestWakeLeavesAnUncleanFirecrackerExitToThePlatform(t *testing.T) {
	stop := &guestStopTestVMM{explanation: errCompanionCrashLooped}
	err := wakeWithCPUPolicyStep(t, errRefusedVsockDial, stop, func(m *Manager, l Lease) { m.ProcessExited(l.Instance, 137) })
	if p := api.AsProblem(err); p != nil {
		t.Fatalf("error = %v, want a platform error, not problem %q", err, p.Code)
	}
	if stop.explained != 0 {
		t.Fatalf("consulted the guest's output %d time(s) for exit code 137", stop.explained)
	}
	if !strings.Contains(err.Error(), "firecracker exited during boot (exit code 137)") {
		t.Fatalf("error = %v, want the exit named", err)
	}
}
