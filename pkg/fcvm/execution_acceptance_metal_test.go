// adr: 171
//go:build linux && metal

package fcvm

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/executionproto"
	"github.com/onebox-faas/faas/pkg/fcvm/leakcheck"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

func executionMetalImages(t *testing.T) (kernel, base, layer string) {
	t.Helper()
	kernel = os.Getenv("FAAS_TEST_KERNEL")
	base = os.Getenv("FAAS_TEST_EXECUTION_BASE_ROOTFS")
	if base == "" {
		base = os.Getenv("FAAS_TEST_BASE_ROOTFS")
	}
	layer = os.Getenv("FAAS_TEST_EXECUTION_LAYER_ROOTFS")
	if kernel == "" || base == "" || layer == "" {
		t.Skip("set FAAS_TEST_KERNEL, FAAS_TEST_EXECUTION_BASE_ROOTFS/FAAS_TEST_BASE_ROOTFS, and FAAS_TEST_EXECUTION_LAYER_ROOTFS to run execution metal acceptance")
	}
	return kernel, base, layer
}

func executionMetalFCVersion(ctx context.Context) (string, error) {
	if version := os.Getenv("FAAS_TEST_FC_VERSION"); version != "" {
		return version, nil
	}
	return DetectFirecrackerVersion(ctx)
}

func newExecutionMetalManager(t *testing.T, kernel, fcVersion string, backend storage.StorageBackend) *Manager {
	t.Helper()
	vmm := newMetalVMM(t, 30*time.Second)
	if backend != nil {
		vmm = vmm.WithStorage(backend)
	}
	return NewManager(wire.ExecRunner{}, vmm, Paths{Kernel: kernel}, fcVersion, nil, nil)
}

func executionMetalWake(ctx context.Context, t *testing.T, m *Manager, instance, kernel, base, layer string, snapshot *Snapshot) *Instance {
	t.Helper()
	inst, err := m.WakeExecution(ctx, ExecutionWakeRequest{
		Instance:      instance,
		AccountID:     "metal-acceptance",
		Plan:          api.PlanHobby,
		Runtime:       string(api.ExecutionRuntimePython312),
		KernelKey:     kernel,
		BaseKey:       base,
		LayerKey:      layer,
		Snapshot:      snapshot,
		VcpuCount:     2,
		MemSizeMiB:    128,
		CPUMillicores: 500,
	})
	if err != nil {
		t.Fatalf("WakeExecution %s: %v", instance, err)
	}
	if inst == nil || !inst.ExecutionOnly || !inst.Lease.Networkless {
		t.Fatalf("execution instance = %#v, want execution-only networkless lifecycle", inst)
	}
	if inst.Net.Tap != "" || inst.Net.Netns != "" || inst.Net.VethHost != "" || inst.Net.VethPeer != "" {
		t.Fatalf("execution instance carried tenant network state: %+v", inst.Net)
	}
	return inst
}

func executeMetalRequest(ctx context.Context, t *testing.T, m *Manager, instance string) executionproto.Result {
	t.Helper()
	result, err := m.ExecuteExecution(ctx, instance, executionproto.Request{
		Version:     executionproto.Version,
		ExecutionID: instance + "-request",
		Runtime:     api.ExecutionRuntimePython312,
		Source:      "def main(value, context): return {'ignored': value}",
		Input:       json.RawMessage(`{"value":42}`),
		TimeoutMS:   2000,
		MaxOutput:   4096,
		NetworkMode: api.ExecutionNetworkNone,
	})
	if err != nil {
		t.Fatalf("ExecuteExecution %s: %v", instance, err)
	}
	if result.Status != api.ExecutionStatusSucceeded {
		t.Fatalf("execution result status = %q, want succeeded (failure=%q)", result.Status, result.FailureCode)
	}
	if string(result.Result) != `{"ok":true,"fixture":"native"}` {
		t.Fatalf("execution result = %s, want native fixture result", result.Result)
	}
	return result
}

// TestMetalExecutionColdBootAndOneShotTeardown is the real-KVM acceptance
// for the disposable execution path. It proves that an execution VM can boot
// without a tenant network, complete one protocol exchange, and release its
// VM, lease, and host resources before returning the result.
func TestMetalExecutionColdBootAndOneShotTeardown(t *testing.T) {
	kernel, base, layer := executionMetalImages(t)
	withCgroupRootAt(t, "/sys/fs/cgroup")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	fcVersion, err := executionMetalFCVersion(ctx)
	if err != nil {
		t.Fatalf("detect Firecracker version: %v", err)
	}
	m := newExecutionMetalManager(t, kernel, fcVersion, nil)

	inst := executionMetalWake(ctx, t, m, "exec-metal-cold", kernel, base, layer, nil)
	if m.LiveCount() != 1 {
		t.Fatalf("live=%d after execution wake, want 1", m.LiveCount())
	}
	_ = executeMetalRequest(ctx, t, m, inst.Lease.Instance)
	if m.LiveCount() != 0 || m.LeasedCount() != 0 {
		t.Fatalf("after one-shot execution live=%d leased=%d, want 0/0", m.LiveCount(), m.LeasedCount())
	}
	leakcheck.AssertZero(t)
}

// TestMetalExecutionSnapshotRestoreAndOneShotTeardown proves the runtime
// snapshot path is also disposable. The prime guest is snapshotted while its
// one-shot listener is waiting; restore must re-seed through the resume hook,
// then exactly one execution is accepted and the restored VM is destroyed.
func TestMetalExecutionSnapshotRestoreAndOneShotTeardown(t *testing.T) {
	kernel, base, layer := executionMetalImages(t)
	withCgroupRootAt(t, "/sys/fs/cgroup")
	store, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatalf("snapshot storage: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	fcVersion, err := executionMetalFCVersion(ctx)
	if err != nil {
		t.Fatalf("detect Firecracker version: %v", err)
	}
	m := newExecutionMetalManager(t, kernel, fcVersion, store)

	prime := executionMetalWake(ctx, t, m, "exec-metal-prime", kernel, base, layer, nil)
	snapshotSpec := SnapshotSpec{
		StorageKey:        "snap/execution-metal/mem",
		VMStateStorageKey: "snap/execution-metal/vmstate",
		VMStatePath:       filepath.Join(t.TempDir(), "legacy-vmstate"),
	}
	if _, err := m.Park(ctx, prime.Lease.Instance, snapshotSpec); err != nil {
		t.Fatalf("park execution prime: %v", err)
	}
	if m.LiveCount() != 0 || m.LeasedCount() != 0 {
		t.Fatalf("after prime park live=%d leased=%d, want 0/0", m.LiveCount(), m.LeasedCount())
	}

	snapshot := &Snapshot{
		FCVersion:         fcVersion,
		StorageKey:        snapshotSpec.StorageKey,
		VMStateStorageKey: snapshotSpec.VMStateStorageKey,
		Networkless:       true,
	}
	restored := executionMetalWake(ctx, t, m, "exec-metal-restore", kernel, base, layer, snapshot)
	if restored.Method != WakeRestore {
		t.Fatalf("execution restore method = %s, want restore", restored.Method)
	}
	_ = executeMetalRequest(ctx, t, m, restored.Lease.Instance)
	if m.LiveCount() != 0 || m.LeasedCount() != 0 {
		t.Fatalf("after restored one-shot execution live=%d leased=%d, want 0/0", m.LiveCount(), m.LeasedCount())
	}
	leakcheck.AssertZero(t)
}
