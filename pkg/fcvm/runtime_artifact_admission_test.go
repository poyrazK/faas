// adr: 593
package fcvm

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

type runtimeSourceCapableVMM struct {
	*fakeVMM
	verifiedCalls int
	verifyErr     error
}

func (v *runtimeSourceCapableVMM) BootColdBootVerified(ctx context.Context, lease Lease, spec ColdBootSpec, _ []runtimeadmission.ArtifactSource) error {
	v.verifiedCalls++
	if v.verifyErr != nil {
		return v.verifyErr
	}
	return v.BootColdBoot(ctx, lease, spec)
}

func admittedSourceFixture(t *testing.T, m *Manager) AdmittedWakeRequest {
	t.Helper()
	r := admittedFixture(t, m)
	r.Request.BaseKey, r.Request.LayerKey = "base/a.ext4", "rootfs/main.ext4"
	r.Request.ArtifactSources = []runtimeadmission.ArtifactSource{runtimeSourceFixture("base-image", "", r.Request.BaseKey, []byte("base")), runtimeSourceFixture("app-layer", "", r.Request.LayerKey, []byte("main"))}
	r.NativeInputHash, _ = NativeWakeInputHash(r.Request)
	return r
}

func TestNativeRuntimeSourcesRequireExplicitBackendBeforeAllocation(t *testing.T) {
	vmm := &fakeVMM{}
	m := newTestManager(&fakeRunner{}, vmm)
	r := admittedSourceFixture(t, m)
	if _, _, err := m.WakeAdmitted(t.Context(), r, nil); !errors.Is(err, runtimeadmission.ErrUnavailable) {
		t.Fatalf("old backend err=%v", err)
	}
	if m.LiveCount() != 0 || m.LeasedCount() != 0 || vmm.bootCount != 0 {
		t.Fatal("missing source capability allocated resources")
	}
	if _, err := m.Wake(t.Context(), r.Request); !errors.Is(err, runtimeadmission.ErrInvalid) {
		t.Fatal("ordinary wake accepted admitted source authority")
	}
}

func TestNativeRuntimeSourcesUseVerifiedColdBootAndRefusePausedSnapshot(t *testing.T) {
	for _, paused := range []bool{false, true} {
		t.Run(map[bool]string{false: "cold fallback", true: "paused refused"}[paused], func(t *testing.T) {
			vmm := &runtimeSourceCapableVMM{fakeVMM: &fakeVMM{}}
			m := newTestManager(&fakeRunner{}, vmm)
			r := admittedSourceFixture(t, m)
			r.Request.Snapshot = &Snapshot{DeploymentID: r.Binding.DeploymentID, FCVersion: m.fcVersion, VMStatePath: "snapshot-state", StorageKey: "snap/" + r.Binding.DeploymentID + "/mem"}
			r.Request.KeepPaused = paused
			r.NativeInputHash, _ = NativeWakeInputHash(r.Request)
			inst, receipt, err := m.WakeAdmitted(t.Context(), r, nil)
			if paused {
				if !errors.Is(err, runtimeadmission.ErrUnavailable) || m.LeasedCount() != 0 || vmm.verifiedCalls != 0 {
					t.Fatalf("unbound paused restore err=%v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if inst.Method != WakeColdBoot || receipt.Method.String() != "WAKE_COLD_BOOT" || vmm.verifiedCalls != 1 || len(vmm.restored) != 0 {
				t.Fatal("snapshot bypassed verified source boot")
			}
			if err := m.Destroy(t.Context(), r.Request.Instance); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNativeRuntimeSourcesVerificationFailureReleasesLease(t *testing.T) {
	verifyErr := errors.New("source byte mismatch")
	vmm := &runtimeSourceCapableVMM{fakeVMM: &fakeVMM{}, verifyErr: verifyErr}
	m := newTestManager(&fakeRunner{}, vmm)
	r := admittedSourceFixture(t, m)
	if _, _, err := m.WakeAdmitted(t.Context(), r, nil); !errors.Is(err, verifyErr) {
		t.Fatalf("verification err=%v", err)
	}
	if m.LiveCount() != 0 || m.LeasedCount() != 0 || vmm.bootCount != 0 || vmm.verifiedCalls != 1 {
		t.Fatal("failed verification retained native resources")
	}
}

func TestVerifiedColdBootStagesCompleteDistinctSourceSet(t *testing.T) {
	sources := []runtimeadmission.ArtifactSource{runtimeSourceFixture("base-image", "", "base/a.ext4", []byte("base")), runtimeSourceFixture("app-layer", "", "rootfs/main.ext4", []byte("main")), runtimeSourceFixture("sidecar-layer", "cache", "rootfs/cache.ext4", []byte("cache"))}
	backend := &runtimeSourceTestBackend{data: map[string][]byte{"base/a.ext4": []byte("base"), "rootfs/main.ext4": []byte("main"), "rootfs/cache.ext4": []byte("cache")}}
	v := &JailerVMM{storage: backend}
	lease := Lease{Instance: "verified-set"}
	t.Cleanup(func() { _ = v.releaseRuntimeSources(lease.Instance) })
	spec := ColdBootSpec{KernelKey: "kernel/vmlinux", BaseKey: sources[0].StorageKey, VcpuCount: 2, MemSizeMiB: 128, Tap: "tap0", Workloads: []WorkloadSpec{{Name: "main", StorageKey: sources[1].StorageKey}, {Name: "cache", StorageKey: sources[2].StorageKey}}}
	prepared, err := v.prepareVerifiedColdBoot(t.Context(), lease, spec, sources)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range []struct{ path, body string }{{prepared.BaseKey, "base"}, {prepared.Workloads[0].StorageKey, "main"}, {prepared.Workloads[1].StorageKey, "cache"}} {
		body, err := os.ReadFile(file.path)
		if err != nil || string(body) != file.body {
			t.Fatal("wrong protected drive bytes")
		}
	}
	if spec.BaseKey != sources[0].StorageKey || spec.Workloads[0].StorageKey != sources[1].StorageKey {
		t.Fatal("staging mutated the caller's source set")
	}
	config := BuildColdBootConfig(prepared, 0)
	if len(config.Drives) != 3 || !config.Drives[0].IsReadOnly || config.Drives[1].IsReadOnly || !config.Drives[2].IsReadOnly {
		t.Fatal("two-drive and sidecar permissions changed")
	}
}

func TestVerifiedColdBootCorruptLaterDriveSweepsEarlierSources(t *testing.T) {
	sources := []runtimeadmission.ArtifactSource{runtimeSourceFixture("base-image", "", "base/a.ext4", []byte("base")), runtimeSourceFixture("app-layer", "", "rootfs/main.ext4", []byte("approved main"))}
	backend := &runtimeSourceTestBackend{data: map[string][]byte{"base/a.ext4": []byte("base"), "rootfs/main.ext4": []byte("bad main")}}
	v := &JailerVMM{storage: backend}
	spec := ColdBootSpec{KernelKey: "kernel/vmlinux", BaseKey: sources[0].StorageKey, LayerKey: sources[1].StorageKey, VcpuCount: 2, MemSizeMiB: 128, Tap: "tap0"}
	if err := v.BootColdBootVerified(t.Context(), Lease{Instance: "corrupt-drive"}, spec, sources); !errors.Is(err, runtimeadmission.ErrInvalid) {
		t.Fatalf("corrupt stream err=%v", err)
	}
	if backend.gets.Load() != 2 || v.runtimeSources().root != "" || len(v.runtimeSources().entries) != 0 {
		t.Fatal("partial source preparation leaked earlier verified blobs or reached kernel staging")
	}
}
