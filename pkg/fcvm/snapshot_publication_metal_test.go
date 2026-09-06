//go:build linux && metal

package fcvm

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

type failedMemoryPublication struct {
	storage.StorageBackend
	failKey string
}

func (s *failedMemoryPublication) Put(ctx context.Context, key string, r io.Reader) error {
	if key == s.failKey {
		return context.DeadlineExceeded
	}
	return s.StorageBackend.Put(ctx, key, r)
}

// Reproduces the live failure boundary: device state has been uploaded when
// the memory upload times out. The earlier capture must still restore, and
// the failed capture must leave neither a live guest nor orphaned objects.
func TestMetalSnapshotPublicationFailurePreservesPrevious(t *testing.T) {
	helper := os.Getenv("FAAS_TEST_VMMD_HELPER")
	if helper == "" {
		t.Skip("requires isolated metal fixture and helper")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	kernel, base, layer := metalImages(t)
	runner := wire.ExecRunner{}
	for _, argv := range [][]string{
		{"ip", "link", "add", netns.TenantBridge, "type", "bridge"},
		{"ip", "addr", "add", "10.100.0.1/16", "dev", netns.TenantBridge},
		{"ip", "link", "set", netns.TenantBridge, "up"},
	} {
		if err := runner.Run(ctx, argv); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = runner.Run(context.Background(), []string{"ip", "link", "del", netns.TenantBridge}) })
	local, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	backend := &failedMemoryPublication{StorageBackend: local}
	version, err := DetectFirecrackerVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	vmm := NewJailerVMM(JailChrootBase, 30*time.Second).WithStorage(backend)
	vmm.mountHelperPath = helper
	m := NewManager(runner, vmm, Paths{Kernel: kernel}, version, nil, nil)
	m.alloc.free = []int{MaxSlots - 1, MaxSlots - 2}
	withCgroupRootAt(t, "/sys/fs/cgroup")
	for _, name := range []string{"optprime", "opta", "optb"} {
		t.Cleanup(func() { _, _, _ = m.SignalAndKill(context.Background(), name, 0, 0) })
	}
	if _, err := m.ColdBoot(ctx, ColdBootRequest{Instance: "optprime", BaseKey: base, LayerKey: layer, VcpuCount: 1, MemSizeMiB: 128, Plan: "hobby"}); err != nil {
		t.Fatal(err)
	}
	memKey := state.SnapshotCaptureMemKey("opt", "init", "first")
	stateKey := state.SnapshotVMStateKey(state.Snapshot{StorageKey: memKey})
	if _, err := m.Park(ctx, "optprime", SnapshotSpec{StorageKey: memKey, VMStateStorageKey: stateKey}); err != nil {
		t.Fatal(err)
	}
	digest := func(key string) [32]byte {
		t.Helper()
		rc, err := local.Get(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		defer rc.Close()
		h := sha256.New()
		if _, err := io.Copy(h, rc); err != nil {
			t.Fatal(err)
		}
		var out [32]byte
		copy(out[:], h.Sum(nil))
		return out
	}
	memHash, stateHash := digest(memKey), digest(stateKey)
	snap := &Snapshot{FCVersion: version, StorageKey: memKey, VMStateStorageKey: stateKey}
	wake := func(name string) *Instance {
		t.Helper()
		ins, err := m.Wake(ctx, WakeRequest{Instance: name, BaseKey: base, LayerKey: layer, VcpuCount: 1, MemSizeMiB: 128, Plan: "hobby", Snapshot: snap})
		if err != nil {
			t.Fatal(err)
		}
		if ins.Method != WakeRestore {
			t.Fatalf("cold fallback: %s", ins.RestoreError)
		}
		return ins
	}
	first := wake("opta")
	firstUUID := fetchV6UUID(t, first.Lease.HostIP.String())
	backend.failKey = state.SnapshotCaptureMemKey("opt", "init", "second")
	failedState := state.SnapshotVMStateKey(state.Snapshot{StorageKey: backend.failKey})
	if _, err := m.Park(ctx, "opta", SnapshotSpec{StorageKey: backend.failKey, VMStateStorageKey: failedState}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected memory publication deadline, got %v", err)
	}
	if digest(memKey) != memHash || digest(stateKey) != stateHash {
		t.Fatal("previous capture changed")
	}
	for _, key := range []string{backend.failKey, failedState} {
		rc, err := local.Get(ctx, key)
		if err == nil {
			_ = rc.Close()
			t.Fatalf("orphaned failed capture %s", key)
		}
		if !storage.IsNotFound(err) {
			t.Fatal(err)
		}
	}
	second := wake("optb")
	secondUUID := fetchV6UUID(t, second.Lease.HostIP.String())
	if firstUUID == "" || firstUUID == secondUUID {
		t.Fatal("restore did not reseed identity")
	}
	if err := m.Destroy(ctx, "optb"); err != nil {
		t.Fatal(err)
	}
	if m.LiveCount() != 0 || m.LeasedCount() != 0 {
		t.Fatalf("leaked live=%d leases=%d", m.LiveCount(), m.LeasedCount())
	}
}
