//go:build metal && linux && amd64

// adr: 435. Requires the dedicated native KVM acceptance host.

// adr: 592
package fcvm

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm/leakcheck"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

type metalRuntimeDriveFixture struct {
	manager *Manager
	vmm     *JailerVMM
	backend storage.StorageBackend
	sources []runtimeadmission.ArtifactSource
}

func TestMetalRuntimeDriveHandoffSharedBasePrivateMainAndSidecar(t *testing.T) {
	f := newMetalRuntimeDriveFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	one := f.boot(t, ctx)
	two := f.boot(t, ctx)
	first, err := f.vmm.runtimeDriveHandoff(one.Lease)
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.vmm.runtimeDriveHandoff(two.Lease)
	if err != nil || first == nil || second == nil {
		t.Fatal("missing native handoffs", err)
	}
	if !os.SameFile(first.drives[0].info, second.drives[0].info) || os.SameFile(first.drives[1].info, second.drives[1].info) {
		t.Fatal("shared read-only base or private writable main contract changed")
	}
	if err := f.manager.Destroy(ctx, one.Lease.Instance); err != nil {
		t.Fatal(err)
	}
	assertMetalRuntimeDriveObservation(t, ctx, f, two)
	if err := f.manager.Destroy(ctx, two.Lease.Instance); err != nil {
		t.Fatal(err)
	}
	if _, err := f.vmm.ObservedRuntimeDrives(ctx, two.Lease); err == nil {
		t.Fatal("destroyed lease retained native drive authority")
	}
	if f.manager.LiveCount() != 0 || f.manager.LeasedCount() != 0 || len(f.vmm.runtimeDriveHandoffs) != 0 || f.vmm.runtimeSources().root != "" {
		t.Fatal("native drive teardown retained runtime ownership")
	}
}

// Source-kind handoff uses actual native drive handles and two live consumers.
// The generic ext4 fixtures do not prove the source build/runner/scanner pipeline.
func TestMetalSourceNativeDriveHandoffKinds(t *testing.T) {
	for _, kind := range []string{"source-app-layer", "function-layer"} {
		t.Run(kind, func(t *testing.T) {
			f := newMetalRuntimeDriveFixture(t)
			f.sources[1].Kind = kind
			ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
			defer cancel()
			one, two := f.boot(t, ctx), f.boot(t, ctx)
			first, err := f.vmm.runtimeDriveHandoff(one.Lease)
			if err != nil || first == nil {
				t.Fatal(err)
			}
			second, err := f.vmm.runtimeDriveHandoff(two.Lease)
			if err != nil || second == nil || first.drives[1].observation.Source.Kind != kind || second.drives[1].observation.Source.Kind != kind ||
				!os.SameFile(first.drives[0].info, second.drives[0].info) || os.SameFile(first.drives[1].info, second.drives[1].info) {
				t.Fatal("native source identity, shared base or private main changed", err)
			}
			if err := f.manager.Destroy(ctx, one.Lease.Instance); err != nil {
				t.Fatal(err)
			}
			assertMetalRuntimeDriveObservation(t, ctx, f, two)
		})
	}
}

func newMetalRuntimeDriveFixture(t *testing.T) metalRuntimeDriveFixture {
	t.Helper()
	for _, name := range []string{"FAAS_TEST_KERNEL", "FAAS_TEST_BASE_ROOTFS", "FAAS_TEST_LAYER_ROOTFS"} {
		file, err := os.Open(os.Getenv(name))
		if err != nil {
			t.Fatalf("native standards acceptance requires readable %s: %v", name, err)
		}
		_ = file.Close()
	}
	kernel, base, layer := metalImages(t)
	withCgroupRootAt(t, "/sys/fs/cgroup")
	root := t.TempDir()
	backend, err := storage.NewLocalStorageBackend(root)
	if err != nil {
		t.Fatal(err)
	}
	sources := []runtimeadmission.ArtifactSource{
		putMetalRuntimeDrive(t, backend, base, "base-image", "", "base/approved.ext4"),
		putMetalRuntimeDrive(t, backend, layer, "app-layer", "", "rootfs/approved.ext4"),
		putMetalRuntimeDrive(t, backend, ensureSidecarExt4(t, root, "metrics", 9090), "sidecar-layer", "metrics", "sidecars/metrics.ext4"),
	}
	v := newMetalVMM(t, 30*time.Second).WithStorage(backend)
	m := NewManager(wire.ExecRunner{}, v, Paths{Kernel: kernel}, os.Getenv("FAAS_TEST_FC_VERSION"), nil, nil)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		for id := range m.LiveInstances() {
			if err := m.Destroy(ctx, id); err != nil {
				t.Error(err)
			}
		}
		leakcheck.AssertZero(t)
	})
	return metalRuntimeDriveFixture{manager: m, vmm: v, backend: backend, sources: sources}
}

func putMetalRuntimeDrive(t *testing.T, backend storage.StorageBackend, path, kind, workload, key string) runtimeadmission.ArtifactSource {
	t.Helper()
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	identity, err := rootfs.ReadArtifactIdentity(t.Context(), file)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	if err := backend.Put(t.Context(), key, file); err != nil {
		t.Fatal(err)
	}
	return runtimeadmission.ArtifactSource{Kind: kind, WorkloadName: workload, StorageKey: key, Digest: identity.Digest, Bytes: identity.Bytes}
}

func (f metalRuntimeDriveFixture) boot(t *testing.T, ctx context.Context) *Instance {
	t.Helper()
	req := f.request(t)
	inst, receipt, err := f.manager.WakeAdmitted(ctx, req, nil)
	if err != nil || receipt.Check(req.Binding, time.Now()) != nil || inst == nil || receipt.Binding.ProtocolVersion != runtimeadmission.ArtifactProtocolVersion || len(receipt.ArtifactConsumption.Drives) != len(f.sources) {
		t.Fatal("verified native boot failed", err)
	}
	assertMetalRuntimeDriveObservation(t, ctx, f, inst)
	if _, err := waitForSidecarHTTP(ctx, inst.Lease.Netns, 9090, "/"); err != nil {
		t.Fatal("verified sidecar drive did not serve its workload", err)
	}
	return inst
}

func (f metalRuntimeDriveFixture) request(t *testing.T) AdmittedWakeRequest {
	t.Helper()
	req := admittedFixture(t, f.manager)
	req.Request.BaseKey, req.Request.LayerKey = f.sources[0].StorageKey, f.sources[1].StorageKey
	req.Request.ArtifactSources = f.sources
	req.Request.Sidecars = []WorkloadSpec{{Name: "metrics", Type: "sidecar", StorageKey: f.sources[2].StorageKey, DriveID: "layer-sidecar-0", RamMB: 64, Port: 9090, Essential: true}}
	req.Request.MemSizeMiB = 256
	var err error
	req.Binding.ProtocolVersion = runtimeadmission.ArtifactProtocolVersion
	req.Binding.ArtifactSourcesHash, err = runtimeadmission.HashArtifactSources(f.sources)
	if err != nil {
		t.Fatal(err)
	}
	req.NativeInputHash, err = NativeWakeInputHash(req.Request)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func assertMetalRuntimeDriveObservation(t *testing.T, ctx context.Context, f metalRuntimeDriveFixture, inst *Instance) {
	t.Helper()
	observation, err := f.vmm.ObservedRuntimeDrives(ctx, inst.Lease)
	if err != nil || observation.InstanceID != inst.Lease.Instance || observation.LeaseUID != inst.Lease.UID || observation.ProcessPID <= 0 || observation.ProcessStart == "" || !runtimeadmission.ValidHash(observation.ConfigHash) || len(observation.Drives) != 3 {
		t.Fatalf("actual native drive observation invalid: %v", err)
	}
	for i, drive := range observation.Drives {
		if drive.Source != f.sources[i] || drive.Producer.Digest != drive.Source.Digest || drive.Producer.Bytes != drive.Source.Bytes || drive.Injected.Bytes != drive.Source.Bytes || drive.ReadOnly != (i != 1) {
			t.Fatal("native drive facts do not match the exact producer set")
		}
		if drive.ReadOnly && drive.Injected != drive.Producer {
			t.Fatal("read-only drive bytes changed")
		}
		if !drive.ReadOnly && drive.Injected == drive.Producer {
			t.Fatal("runtime injection was not measured separately from producer bytes")
		}
	}
}

func TestMetalRuntimeDriveHandoffRefusesChangedSourceBeforeLaunch(t *testing.T) {
	f := newMetalRuntimeDriveFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	req := f.request(t)
	if err := f.backend.Put(ctx, f.sources[1].StorageKey, bytes.NewReader([]byte("changed mutable source"))); err != nil {
		t.Fatal(err)
	}
	inst, receipt, err := f.manager.WakeAdmitted(ctx, req, nil)
	if !errors.Is(err, runtimeadmission.ErrInvalid) || inst != nil || receipt.Binding.Token != "" {
		t.Fatal("changed source reached native runtime admission", err)
	}
	if f.manager.LiveCount() != 0 || f.manager.LeasedCount() != 0 || len(f.vmm.proc) != 0 || len(f.vmm.runtimeDriveHandoffs) != 0 || f.vmm.runtimeSources().root != "" {
		t.Fatal("refused native source retained resources")
	}
}
