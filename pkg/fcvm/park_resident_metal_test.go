//go:build metal

package fcvm

// spec: §4.4 — a full snapshot charges the resident guest plus the memory file
// Firecracker writes into the jail tmpfs; parking must not OOM either way.

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

// TestMetalParkGuestWithLargeResidentSet parks a 1 GiB guest whose app holds
// more resident memory than SnapshotVMOverheadMB. Before the snapshot fence
// covered the memory file, such a guest was OOM-killed mid-capture.
//
// FAAS_TEST_RESIDENT_LAYER names a layer whose app holds ~300 MiB resident
// before it starts listening (the n2 runner builds one with busybox).
func TestMetalParkGuestWithLargeResidentSet(t *testing.T) {
	kernel, base, _ := metalImages(t)
	layer := os.Getenv("FAAS_TEST_RESIDENT_LAYER")
	if layer == "" {
		t.Skip("set FAAS_TEST_RESIDENT_LAYER to a layer whose app holds ~300 MiB resident")
	}
	fcVersion, err := DetectFirecrackerVersion(context.Background())
	if err != nil {
		t.Fatalf("detect firecracker version: %v", err)
	}
	work := t.TempDir()
	if err := os.MkdirAll(filepath.Join(work, "store", "snap"), 0o2770); err != nil {
		t.Fatal(err)
	}
	store, err := storage.NewLocalStorageBackend(filepath.Join(work, "store"))
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(wire.ExecRunner{}, newMetalVMM(t, 90*time.Second).WithStorage(store),
		Paths{Kernel: kernel}, fcVersion, nil, nil)
	withCgroupRootAt(t, "/sys/fs/cgroup")

	ctx := context.Background()
	const instance = "resident"
	snap := &Snapshot{FCVersion: fcVersion, StorageKey: "snap/resident/mem", VMStateStorageKey: "snap/resident/vmstate", VMStatePath: filepath.Join(work, "vmstate")}
	spec := SnapshotSpec{VMStatePath: snap.VMStatePath, StorageKey: snap.StorageKey, VMStateStorageKey: snap.VMStateStorageKey}
	req := ColdBootRequest{Instance: instance, Plan: "scale", BaseKey: base, LayerKey: layer, VcpuCount: 2, MemSizeMiB: 1024}
	if _, err := m.ColdBoot(ctx, req); err != nil {
		t.Fatalf("cold boot: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), instance) })
	if _, err := m.Park(ctx, instance, spec); err != nil {
		t.Fatalf("park a guest with a large resident set: %v", err)
	}
	memPath, _, err := store.LocalPath(snap.StorageKey)
	if err != nil {
		t.Fatal(err)
	}
	if content := nonZeroPages(t, memPath); content < 280<<20 {
		t.Fatalf("snapshot holds %d MiB of content; the fixture must keep >280 MiB resident", content>>20)
	} else {
		t.Logf("parked a guest holding %d MiB of non-zero memory", content>>20)
	}
	inst, err := m.Wake(ctx, WakeRequest{Instance: instance, Plan: "scale", BaseKey: base, LayerKey: layer, VcpuCount: 2, MemSizeMiB: 1024, Snapshot: snap})
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	if inst.Method != WakeRestore {
		t.Fatalf("woke by %s, want a snapshot restore", inst.Method)
	}
}

// nonZeroPages counts the bytes of path's 4 KiB pages that hold data.
func nonZeroPages(t *testing.T, path string) int64 {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:forbidigo // test-owned snapshot file.
	if err != nil {
		t.Fatal(err)
	}
	var n int64
	for i := 0; i+4096 <= len(data); i += 4096 {
		for _, b := range data[i : i+4096] {
			if b != 0 {
				n += 4096
				break
			}
		}
	}
	return n
}
