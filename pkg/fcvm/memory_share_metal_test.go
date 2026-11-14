//go:build metal

package fcvm

// adr: 956 — a VM restores from a memory file whose pages share image blocks,
// and from one that shares runs with another memory file (amendment 2026-10-31).

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
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
	work := t.TempDir()
	kernel, base, layer := memoryShareMetalImages(t, work)
	fcVersion, err := DetectFirecrackerVersion(context.Background())
	if err != nil {
		t.Fatalf("detect firecracker version: %v", err)
	}
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

// TestMetalWakeAfterMemoryPeerSharing covers the ADR-956 amendment of
// 2026-10-31: a memory file shares runs with another memory file on the node.
// Two guests boot from the same images and park. The second's memory file is
// shared with its images, then with the first's memory file, as snapshothipd
// does. Both must then restore from the shared files, and sharing must not
// change a byte of either. Restore times before and after sharing are logged,
// cold (page cache dropped) and warm, as evidence for the restore budget.
// Run with TMPDIR on XFS (reflink=1).
func TestMetalWakeAfterMemoryPeerSharing(t *testing.T) {
	work := t.TempDir()
	kernel, base, layer := memoryShareMetalImages(t, work)
	fcVersion, err := DetectFirecrackerVersion(context.Background())
	if err != nil {
		t.Fatalf("detect firecracker version: %v", err)
	}
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

	type guest struct {
		instance string
		snap     *Snapshot
		mem      string
	}
	park := func(instance string) guest {
		snap := &Snapshot{
			FCVersion:         fcVersion,
			StorageKey:        "snap/" + instance + "/mem",
			VMStateStorageKey: "snap/" + instance + "/vmstate",
			VMStatePath:       filepath.Join(work, instance+".vmstate"),
		}
		spec := SnapshotSpec{VMStatePath: snap.VMStatePath, StorageKey: snap.StorageKey, VMStateStorageKey: snap.VMStateStorageKey}
		if _, err := m.ColdBoot(ctx, ColdBootRequest{
			Instance: instance, Plan: "pro", BaseKey: base, LayerKey: layer, VcpuCount: 2, MemSizeMiB: 128,
		}); err != nil {
			t.Fatalf("%s: cold boot: %v", instance, err)
		}
		t.Cleanup(func() { _ = m.Destroy(context.Background(), instance) })
		if _, err := m.Park(ctx, instance, spec); err != nil {
			t.Fatalf("%s: park: %v", instance, err)
		}
		mem, ok, err := store.LocalPath(snap.StorageKey)
		if err != nil || !ok {
			t.Fatalf("%s: resolve memory file: ok=%v err=%v", instance, ok, err)
		}
		return guest{instance: instance, snap: snap, mem: mem}
	}
	// wake restores g from its parked capture and stops it again without a
	// new capture, so every wake reads the same memory file, as repeated
	// wakes of a parked app do.
	wake := func(g guest, cold bool) time.Duration {
		if cold {
			dropPageCache(t)
		}
		start := time.Now()
		inst, err := m.Wake(ctx, WakeRequest{
			Instance: g.instance, Plan: "pro", BaseKey: base, LayerKey: layer, VcpuCount: 2, MemSizeMiB: 128, Snapshot: g.snap,
		})
		took := time.Since(start)
		if err != nil {
			t.Fatalf("%s: wake: %v", g.instance, err)
		}
		if inst.Method != WakeRestore {
			t.Fatalf("%s: woke by %s, want a snapshot restore", g.instance, inst.Method)
		}
		if err := m.Destroy(ctx, g.instance); err != nil {
			t.Fatalf("%s: destroy after wake: %v", g.instance, err)
		}
		return took
	}
	// FAAS_MEMORY_SHARE_BENCH_CYCLES raises the restores per measurement for
	// a timing comparison; the default keeps the test short.
	cycles := 5
	if raw := os.Getenv("FAAS_MEMORY_SHARE_BENCH_CYCLES"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			t.Fatalf("FAAS_MEMORY_SHARE_BENCH_CYCLES=%q: want a positive integer", raw)
		}
		cycles = n
	}
	// extents logs how many extents the memory file has: every shared range
	// is one, and a restore reads the file in that layout.
	extents := func(label, path string) {
		out, err := exec.Command("filefrag", path).CombinedOutput()
		if err != nil {
			t.Logf("%s: filefrag unavailable: %v", label, err)
			return
		}
		t.Logf("%s: %s", label, strings.TrimSpace(string(out)))
	}
	measure := func(label string, g guest) {
		for _, cold := range []bool{true, false} {
			var took []time.Duration
			for range cycles {
				took = append(took, wake(g, cold))
			}
			slices.Sort(took)
			kind := "warm"
			if cold {
				kind = "cold"
			}
			t.Logf("%s, %s page cache: wake p50 %s, min %s, max %s over %d restores",
				label, kind, took[len(took)/2].Round(time.Millisecond), took[0].Round(time.Millisecond),
				took[len(took)-1].Round(time.Millisecond), cycles)
		}
	}

	a := park("peer-a")
	b := park("peer-b")
	extents("unshared", b.mem)
	measure("unshared", b)

	digestA, digestB := fileDigest(t, a.mem), fileDigest(t, b.mem)
	imageShared, err := storage.ShareMemoryWithImages(ctx, b.mem, []string{layer, base})
	if errors.Is(err, storage.ErrBlockSharingUnsupported) {
		t.Skipf("TMPDIR %s cannot share blocks; run on XFS with reflink=1", os.TempDir())
	}
	if err != nil {
		t.Fatalf("share memory with images: %v", err)
	}
	extents("shared with images", b.mem)
	measure("shared with images", b)
	peerShared, err := storage.ShareMemoryWithPeers(ctx, b.mem, []string{a.mem})
	if err != nil {
		t.Fatalf("share memory with the other memory file: %v", err)
	}
	if peerShared == 0 {
		t.Fatal("no memory run shared with the other guest's memory file")
	}
	if fileDigest(t, a.mem) != digestA || fileDigest(t, b.mem) != digestB {
		t.Fatal("sharing changed a memory file")
	}
	size, allocated := fileSizes(t, b.mem)
	t.Logf("memory file: %d bytes logical, %d allocated; %d shared with images, %d with the other memory file",
		size, allocated, imageShared, peerShared)

	extents("shared with images and peer", b.mem)
	measure("shared with images and peer", b)
	// The file that holds the shared blocks restores too.
	wake(a, true)
	if fileDigest(t, a.mem) != digestA || fileDigest(t, b.mem) != digestB {
		t.Fatal("a restore changed a shared memory file")
	}
	if m.LeasedCount() != 0 {
		t.Errorf("leaked leases: %d", m.LeasedCount())
	}
}

// memoryShareMetalImages returns the kernel and copies of the base and layer
// in dir: the operator's FAAS_TEST_* fixtures when set, otherwise the
// self-contained v6 fixture built from this checkout, so the memory-sharing
// tests run from a plain `make test-metal` on any acceptance host. The images
// must sit on the same filesystem as the memory files for blocks to be shared.
func memoryShareMetalImages(t *testing.T, dir string) (kernel, base, layer string) {
	t.Helper()
	kernel = os.Getenv("FAAS_TEST_KERNEL")
	if kernel == "" {
		kernel = "/srv/fc/base/vmlinux-6.1.134"
	}
	if _, err := os.Stat(kernel); err != nil {
		t.Skipf("kernel %s unavailable: %v (set FAAS_TEST_KERNEL)", kernel, err)
	}
	base, layer = filepath.Join(dir, "base.ext4"), filepath.Join(dir, "layer.ext4")
	if b, l := os.Getenv("FAAS_TEST_BASE_ROOTFS"), os.Getenv("FAAS_TEST_LAYER_ROOTFS"); b != "" && l != "" {
		return kernel, copyMetalFixture(t, b, base), copyMetalFixture(t, l, layer)
	}
	if err := buildV6BaseExt4(base, repoRoot(t)); err != nil {
		t.Fatalf("build base fixture: %v", err)
	}
	if err := buildV6LayerExt4Size(layer, 64); err != nil {
		t.Fatalf("build layer fixture: %v", err)
	}
	return kernel, base, layer
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
