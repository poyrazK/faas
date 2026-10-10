//go:build metal

package fcvm

// adr: 911 — a VM restores from a memory file whose pages share image blocks.

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

// TestMetalWakeAfterMemorySharing parks a real guest, shares its memory
// file's pages with the base and layer blocks they copied, and restores from
// the shared file. Every wake must be a snapshot restore, and sharing must not
// change a byte of the memory file. Run with TMPDIR on XFS (reflink=1): the
// memory file and the images must share one filesystem that can share blocks.
func TestMetalWakeAfterMemorySharing(t *testing.T) {
	kernel, base, layer := metalImages(t)
	fcVersion, err := DetectFirecrackerVersion(context.Background())
	if err != nil {
		t.Fatalf("detect firecracker version: %v", err)
	}
	work := t.TempDir()
	base = copyMetalFixture(t, base, filepath.Join(work, "base.ext4"))
	layer = copyMetalFixture(t, layer, filepath.Join(work, "layer.ext4"))
	snapshotRoot := filepath.Join(work, "store")
	if err := os.MkdirAll(filepath.Join(snapshotRoot, "snap"), 0o2770); err != nil {
		t.Fatal(err)
	}
	store, err := storage.NewLocalStorageBackend(snapshotRoot)
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(wire.ExecRunner{}, newMetalVMM(t, 30*time.Second).WithStorage(store),
		Paths{Kernel: kernel}, fcVersion, nil, nil)
	withCgroupRootAt(t, "/sys/fs/cgroup")

	ctx := context.Background()
	const instance = "memshare"
	snap := &Snapshot{
		FCVersion:         fcVersion,
		StorageKey:        "snap/memshare/mem",
		VMStateStorageKey: "snap/memshare/vmstate",
		VMStatePath:       filepath.Join(work, "vmstate"),
	}
	spec := SnapshotSpec{VMStatePath: snap.VMStatePath, StorageKey: snap.StorageKey, VMStateStorageKey: snap.VMStateStorageKey}
	if _, err := m.ColdBoot(ctx, ColdBootRequest{
		Instance: instance, Plan: "pro", BaseKey: base, LayerKey: layer, VcpuCount: 2, MemSizeMiB: 128,
	}); err != nil {
		t.Fatalf("cold boot: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), instance) })
	if _, err := m.Park(ctx, instance, spec); err != nil {
		t.Fatalf("park: %v", err)
	}
	memPath, ok, err := store.LocalPath(snap.StorageKey)
	if err != nil || !ok {
		t.Fatalf("resolve memory file: ok=%v err=%v", ok, err)
	}
	size, allocated := fileSizes(t, memPath)
	// The jail is tmpfs, so publication crosses filesystems: the published
	// memory must stay sparse rather than allocate every guest page.
	if allocated >= size*9/10 {
		t.Fatalf("published memory is dense: %d of %d bytes allocated", allocated, size)
	}
	t.Logf("published memory: %d bytes logical, %d allocated", size, allocated)

	const cycles = 10
	for i := range cycles {
		before := fileDigest(t, memPath)
		shared, err := storage.ShareMemoryWithImages(ctx, memPath, []string{layer, base})
		if errors.Is(err, storage.ErrBlockSharingUnsupported) {
			t.Skipf("TMPDIR %s cannot share blocks; run on XFS with reflink=1", os.TempDir())
		}
		if err != nil {
			t.Fatalf("cycle %d: share memory: %v", i, err)
		}
		if shared == 0 {
			t.Fatalf("cycle %d: no memory page shared with the base or layer", i)
		}
		if after := fileDigest(t, memPath); after != before {
			t.Fatalf("cycle %d: sharing changed the memory file", i)
		}
		inst, err := m.Wake(ctx, WakeRequest{
			Instance: instance, Plan: "pro", BaseKey: base, LayerKey: layer, VcpuCount: 2, MemSizeMiB: 128, Snapshot: snap,
		})
		if err != nil {
			t.Fatalf("cycle %d: wake from shared memory: %v", i, err)
		}
		if inst.Method != WakeRestore {
			t.Fatalf("cycle %d: woke by %s, want a snapshot restore", i, inst.Method)
		}
		t.Logf("cycle %d: restored from memory sharing %d bytes with its images", i, shared)
		if _, err := m.Park(ctx, instance, spec); err != nil {
			t.Fatalf("cycle %d: park: %v", i, err)
		}
	}
	if m.LeasedCount() != 0 {
		t.Errorf("leaked leases: %d", m.LeasedCount())
	}
}

func copyMetalFixture(t *testing.T, src, dst string) string {
	t.Helper()
	in, err := os.Open(src) //nolint:forbidigo // operator-supplied metal fixture.
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644) //nolint:forbidigo // test-owned temp path.
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	return dst
}

func fileSizes(t *testing.T, path string) (size, allocated int64) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Size(), info.Sys().(*syscall.Stat_t).Blocks * 512
}

func fileDigest(t *testing.T, path string) [32]byte {
	t.Helper()
	f, err := os.Open(path) //nolint:forbidigo // test-owned snapshot file.
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		t.Fatal(err)
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum
}
