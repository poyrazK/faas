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
	withFakeCgroupRoot(t)
	vmm := &cpuPolicyTestVMM{fakeVMM: &fakeVMM{}, cpuLimitErr: errRefusedVsockDial}
	manager := newTestManager(&fakeRunner{}, vmm)
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
