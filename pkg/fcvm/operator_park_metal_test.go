//go:build linux && metal

package fcvm

// adr: 1525
// Run on the supported native KVM acceptance host.

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm/leakcheck"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

type operatorParkMetalFailureVMM struct{ *JailerVMM }

func (v *operatorParkMetalFailureVMM) Snapshot(ctx context.Context, l Lease, _ SnapshotSpec) (SnapshotInfo, error) {
	if err := v.apiPatch(ctx, l.Instance, "/vm", map[string]any{"state": "Paused"}); err != nil {
		return SnapshotInfo{}, err
	}
	return SnapshotInfo{}, errors.New("injected capture failure after pause")
}

func TestMetalOperatorParkFailureAndGeneration(t *testing.T) {
	kernel, base, layer := metalImages(t)
	withCgroupRootAt(t, "/sys/fs/cgroup")
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	vmm := &operatorParkMetalFailureVMM{JailerVMM: newMetalVMM(t, 30*time.Second)}
	m := NewManager(wire.ExecRunner{}, vmm, Paths{Kernel: kernel}, os.Getenv("FAAS_TEST_FC_VERSION"), nil, nil)
	request := admissionWake("operator-park-generation")
	request.BaseKey, request.LayerKey = base, layer
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := m.Destroy(cleanupCtx, request.Instance); err != nil {
			t.Error(err)
		}
	})
	if _, err := m.Wake(ctx, request); err != nil {
		t.Fatal(err)
	}
	generation, err := m.OperatorRestartGeneration(request.Instance)
	if err != nil {
		t.Fatal(err)
	}
	target := state.OperatorParkTarget{OperatorRestartTarget: state.OperatorRestartTarget{InstanceID: request.Instance, Generation: generation}, Action: state.OperatorParkCapture, FCVersion: m.fcVersion}
	r, err := m.ExecuteOperatorPark(ctx, target)
	if err != nil || r.Outcome != string(state.StateStopped) || m.LiveCount() != 0 || m.LeasedCount() != 0 {
		t.Fatalf("unverified cleanup=%+v %v live=%d leases=%d", r, err, m.LiveCount(), m.LeasedCount())
	}
	if _, err := m.Wake(ctx, request); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ExecuteOperatorPark(ctx, target); err == nil || m.LiveCount() != 1 {
		t.Fatal("old park reached replacement", err)
	}
	if err := m.Destroy(ctx, request.Instance); err != nil {
		t.Fatal(err)
	}
	leakcheck.AssertZero(t)
}

func TestMetalOperatorParkCapture(t *testing.T) {
	kernel, base, layer := metalImages(t)
	withCgroupRootAt(t, "/sys/fs/cgroup")
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	backend, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	vmm := newMetalVMM(t, 30*time.Second).WithStorage(backend)
	m := NewManager(wire.ExecRunner{}, vmm, Paths{Kernel: kernel}, os.Getenv("FAAS_TEST_FC_VERSION"), nil, nil)
	request := admissionWake("operator-park-capture")
	request.BaseKey, request.LayerKey = base, layer
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := m.Destroy(cleanupCtx, request.Instance); err != nil {
			t.Error(err)
		}
	})
	if _, err := m.Wake(ctx, request); err != nil {
		t.Fatal(err)
	}
	generation, err := m.OperatorRestartGeneration(request.Instance)
	if err != nil {
		t.Fatal(err)
	}
	key := "snap/operator-park-capture/mem"
	target := state.OperatorParkTarget{OperatorRestartTarget: state.OperatorRestartTarget{InstanceID: request.Instance, Generation: generation}, Action: state.OperatorParkCapture, MemoryKey: key, VMStateKey: state.SnapshotVMStateKey(state.Snapshot{StorageKey: key}), FCVersion: m.fcVersion}
	r, err := m.ExecuteOperatorPark(ctx, target)
	if err != nil || r.Outcome != string(state.StateParked) || r.MemBytes <= 0 || r.VMStateBytes <= 0 || m.LiveCount() != 0 || m.LeasedCount() != 0 {
		t.Fatalf("native capture=%+v %v live=%d leases=%d", r, err, m.LiveCount(), m.LeasedCount())
	}
	for _, key := range []string{target.MemoryKey, target.VMStateKey, state.SnapshotDriveKey(state.Snapshot{StorageKey: target.MemoryKey})} {
		reader, err := backend.Get(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		if err := reader.Close(); err != nil {
			t.Fatal(err)
		}
	}
	leakcheck.AssertZero(t)
}
