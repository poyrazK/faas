//go:build metal

// adr: 510 — on a real guest, a capture whose base was evicted from the node
// cache restores after the base is fetched; a fetched base whose identity
// differs from the capture's is refused and the wake cold-boots.
package fcvm

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

func TestMetalRestoreFetchesEvictedBase(t *testing.T) {
	kernel := os.Getenv("FAAS_TEST_KERNEL")
	if kernel == "" {
		kernel = "/srv/fc/base/vmlinux-6.1.134"
	}
	if _, err := os.Stat(kernel); err != nil {
		t.Skipf("kernel %s unavailable: %v (set FAAS_TEST_KERNEL)", kernel, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	dir := benchFixtureDir(t)
	baseA := filepath.Join(dir, "base-a.ext4")
	baseB := filepath.Join(dir, "base-b.ext4")
	layer := filepath.Join(dir, "layer.ext4")
	if err := buildV6BaseExt4(baseA, repoRoot(t)); err != nil {
		t.Fatalf("build base: %v", err)
	}
	// Bootable, but a different image: its label differs, so its content does.
	if err := buildV6BaseExt4With(baseB, repoRoot(t), 8080, "faas-v6-b"); err != nil {
		t.Fatalf("build replacement base: %v", err)
	}
	if err := buildV6LayerExt4Size(layer, 64); err != nil {
		t.Fatalf("build layer: %v", err)
	}

	// The canonical store has no local paths, as production's GCS bucket; the
	// node cache over it is the only local copy of the base.
	storeRoot := filepath.Join(dir, "store")
	if err := os.MkdirAll(filepath.Join(storeRoot, "snap"), 0o2770); err != nil {
		t.Fatal(err)
	}
	parent, err := storage.NewLocalStorageBackend(storeRoot)
	if err != nil {
		t.Fatal(err)
	}
	const baseKey = "base/releases/metal-fetch-evicted.ext4"
	putFile := func(key, path string) {
		t.Helper()
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = f.Close() }()
		if err := parent.Put(ctx, key, f); err != nil {
			t.Fatalf("put %s: %v", key, err)
		}
	}
	putFile(baseKey, baseA)
	clean := []byte(`{"image":"metal-fetch","findings":{"CRITICAL":0}}`)
	if err := parent.Put(ctx, wire.ScanKeyForBaseKey(baseKey), bytes.NewReader(clean)); err != nil {
		t.Fatal(err)
	}
	cacheDir := filepath.Join(dir, "node-cache")
	cache, err := storage.NewLocalCacheBackend(remoteOnlyStore{parent}, cacheDir, 64<<30)
	if err != nil {
		t.Fatal(err)
	}
	evictBase := func() {
		t.Helper()
		cached := storage.CacheFileForKey(cacheDir, baseKey)
		for _, p := range []string{cached, cached + ".meta"} {
			if err := os.Remove(p); err != nil {
				t.Fatalf("evict base: %v", err)
			}
		}
	}

	fcVersion, err := DetectFirecrackerVersion(ctx)
	if err != nil {
		t.Fatalf("detect firecracker version: %v", err)
	}
	vmm := newMetalVMM(t, 30*time.Second).WithStorage(cache)
	stageBenchMountHelper(t, vmm)
	warnings := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	m := NewManager(wire.ExecRunner{}, vmm, Paths{Kernel: kernel}, fcVersion, warnings, nil)
	m.WithStorage(cache)
	withCgroupRootAt(t, "/sys/fs/cgroup")

	const instance = "fetch-evicted-base"
	t.Cleanup(func() { _ = m.Destroy(context.Background(), instance) })
	snapDir := t.TempDir()
	snap := &Snapshot{
		FCVersion:         fcVersion,
		StorageKey:        "snap/fetch-evicted-base/mem",
		VMStateStorageKey: "snap/fetch-evicted-base/vmstate",
		VMStatePath:       filepath.Join(snapDir, "vmstate"),
	}
	spec := SnapshotSpec{VMStatePath: snap.VMStatePath, StorageKey: snap.StorageKey, VMStateStorageKey: snap.VMStateStorageKey}
	req := ColdBootRequest{Instance: instance, Plan: "pro", BaseKey: baseKey, LayerKey: layer, VcpuCount: 2, MemSizeMiB: 128}
	if _, err := m.ColdBoot(ctx, req); err != nil {
		t.Fatalf("cold boot: %v", err)
	}
	if _, err := m.Park(ctx, instance, spec); err != nil {
		t.Fatalf("park: %v", err)
	}
	wake := func() WakeMethod {
		t.Helper()
		out, err := m.Wake(ctx, WakeRequest{Instance: instance, Plan: "pro", BaseKey: baseKey, LayerKey: layer,
			VcpuCount: 2, MemSizeMiB: 128, Snapshot: snap})
		if err != nil {
			t.Fatalf("wake: %v", err)
		}
		return out.Method
	}

	evictBase()
	if got := wake(); got != WakeRestore {
		t.Fatalf("wake after base eviction: method = %s, want restore", got)
	}
	if _, _, ok, err := cache.LocalPathWithSource(baseKey); err != nil || !ok {
		t.Fatalf("fetched base not in the node cache: ok=%v err=%v", ok, err)
	}
	if _, err := m.Park(ctx, instance, spec); err != nil {
		t.Fatalf("park after restore: %v", err)
	}

	// The canonical base changes and the node's copy is evicted: the fetch
	// brings the new image, whose identity differs from the capture's.
	putFile(baseKey, baseB)
	evictBase()
	if got := wake(); got != WakeColdBoot {
		t.Fatalf("wake onto a changed base: method = %s, want cold boot", got)
	}
}
