package fcvm

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// flakyResumeVMM fails the first resume-hook dials, as a cold-booting guest
// does before guest-init has bound its listeners.
type flakyResumeVMM struct {
	*fakeVMM
	failures int
	calls    int
}

func (v *flakyResumeVMM) TriggerResumeHook(ctx context.Context, l Lease, hostTimeUnixNano int64) error {
	v.calls++
	if v.failures < 0 || v.calls <= v.failures {
		return errors.New("guest listener not bound yet")
	}
	return v.fakeVMM.TriggerResumeHook(ctx, l, hostTimeUnixNano)
}

func wakeCaptureExecution(t *testing.T, m *Manager, instance string) *Instance {
	t.Helper()
	inst, err := m.WakeExecution(context.Background(), ExecutionWakeRequest{
		Instance: instance, AccountID: "acct-capture", Plan: api.PlanPro,
		Runtime: string(api.ExecutionRuntimeNode22), KernelKey: "kernel/test",
		BaseKey: "base/test", LayerKey: "layer/test", VcpuCount: api.ExecutionVCPUCount,
		MemSizeMiB: 128, CPUMillicores: 250,
	})
	if err != nil {
		t.Fatalf("WakeExecution: %v", err)
	}
	return inst
}

// adr: 171 — a runtime snapshot is captured only after the payload-free guest
// acknowledges the resume hook, which it binds after its execution listener.
func TestManagerParkExecutionWaitsForGuestBeforeCapture(t *testing.T) {
	vmm := &flakyResumeVMM{fakeVMM: &fakeVMM{}, failures: 2}
	m := NewManager(&fakeRunner{}, vmm, Paths{Kernel: "kernel/test"}, "1.0.0", nil, nil)
	inst := wakeCaptureExecution(t, m, "exec-capture-1")

	info, err := m.Park(context.Background(), inst.Lease.Instance, SnapshotSpec{
		StorageKey: "snap/runtime/captures/c1/mem", VMStateStorageKey: "snap/runtime/captures/c1/vmstate",
	})
	if err != nil {
		t.Fatalf("Park: %v", err)
	}
	if info.MemBytes == 0 || vmm.calls != 3 {
		t.Fatalf("mem_bytes=%d resume calls=%d, want capture after the third hook", info.MemBytes, vmm.calls)
	}
	vmm.mu.Lock()
	snapshotted := append([]string(nil), vmm.snapshotted...)
	vmm.mu.Unlock()
	if len(snapshotted) != 1 || snapshotted[0] != inst.Lease.Instance {
		t.Fatalf("snapshotted = %v", snapshotted)
	}
	m.mu.Lock()
	_, live := m.live[inst.Lease.Instance]
	m.mu.Unlock()
	if live {
		t.Fatal("parked capture VM is still live")
	}
}

func TestManagerParkExecutionNeverReadyIsDestroyedWithoutCapture(t *testing.T) {
	vmm := &flakyResumeVMM{fakeVMM: &fakeVMM{}, failures: -1}
	m := NewManager(&fakeRunner{}, vmm, Paths{Kernel: "kernel/test"}, "1.0.0", nil, nil)
	inst := wakeCaptureExecution(t, m, "exec-capture-2")

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err := m.Park(ctx, inst.Lease.Instance, SnapshotSpec{
		StorageKey: "snap/runtime/captures/c2/mem", VMStateStorageKey: "snap/runtime/captures/c2/vmstate",
	}); err == nil {
		t.Fatal("Park captured a guest that never acknowledged the resume hook")
	}
	vmm.mu.Lock()
	snapshotted := len(vmm.snapshotted)
	vmm.mu.Unlock()
	if snapshotted != 0 {
		t.Fatalf("snapshot calls = %d, want 0", snapshotted)
	}
	m.mu.Lock()
	_, live := m.live[inst.Lease.Instance]
	m.mu.Unlock()
	if live {
		t.Fatal("unready capture VM was not destroyed")
	}
}
