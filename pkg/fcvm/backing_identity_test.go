// adr: 510
package fcvm

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

// backingFixture is a Manager with a real storage backend and real kernel and
// base files at absolute paths, so identities are computed from file content.
type backingFixture struct {
	m            *Manager
	vmm          *fakeVMM
	kernel, base string
	dir          string
}

func newBackingFixture(ctx context.Context, t *testing.T) backingFixture {
	t.Helper()
	dir := t.TempDir()
	kernel := filepath.Join(dir, "vmlinux")
	base := filepath.Join(dir, "base.ext4")
	writeFile(t, kernel, "kernel-v1")
	writeFile(t, base, "base-layout-a")
	storeRoot := filepath.Join(dir, "store")
	if err := os.MkdirAll(filepath.Join(storeRoot, "snap"), 0o755); err != nil {
		t.Fatal(err)
	}
	backend, err := storage.NewLocalStorageBackend(storeRoot)
	if err != nil {
		t.Fatal(err)
	}
	// A storage-backed Manager also enforces the base scan gate (#299).
	clean := []byte(`{"image":"test","findings":{"CRITICAL":0}}`)
	if err := backend.Put(ctx, wire.ScanKeyForBaseKey(base), bytes.NewReader(clean)); err != nil {
		t.Fatal(err)
	}
	vmm := &fakeVMM{}
	m := NewManager(&fakeRunner{}, vmm, Paths{Kernel: kernel}, testFCVersion, nil, nil)
	m.WithStorage(backend)
	return backingFixture{m: m, vmm: vmm, kernel: kernel, base: base, dir: dir}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// replaceFile mirrors a cache refresh: write a temp file, rename it over.
func replaceFile(t *testing.T, path, content string) {
	t.Helper()
	writeFile(t, path+".refresh", content)
	if err := os.Rename(path+".refresh", path); err != nil {
		t.Fatal(err)
	}
}

func (f backingFixture) capture(ctx context.Context, t *testing.T, memKey string) *Snapshot {
	t.Helper()
	f.m.rememberInstanceBacking("i", f.base)
	f.m.writeSnapshotBacking(ctx, "i", memKey)
	return &Snapshot{FCVersion: testFCVersion, StorageKey: memKey, VMStatePath: "/snap/state"}
}

func TestVerifySnapshotBacking(t *testing.T) {
	ctx := context.Background()
	t.Run("same images restore", func(t *testing.T) {
		f := newBackingFixture(ctx, t)
		snap := f.capture(ctx, t, "snap/d1/captures/c1/v2/mem")
		if err := f.m.verifySnapshotBacking(ctx, snap, f.base); err != nil {
			t.Fatalf("verify = %v, want nil", err)
		}
	})
	t.Run("identical content under a new inode restores", func(t *testing.T) {
		// Identity is content, so another node's copy of the same image
		// (or a refresh that re-downloads identical bytes) still restores.
		f := newBackingFixture(ctx, t)
		snap := f.capture(ctx, t, "snap/d1/captures/c1/v2/mem")
		replaceFile(t, f.base, "base-layout-a")
		if err := f.m.verifySnapshotBacking(ctx, snap, f.base); err != nil {
			t.Fatalf("verify = %v, want nil", err)
		}
	})
	t.Run("replaced base is refused", func(t *testing.T) {
		f := newBackingFixture(ctx, t)
		snap := f.capture(ctx, t, "snap/d1/captures/c1/v2/mem")
		replaceFile(t, f.base, "base-layout-b")
		if err := f.m.verifySnapshotBacking(ctx, snap, f.base); !errors.Is(err, ErrSnapshotBackingChanged) {
			t.Fatalf("verify = %v, want ErrSnapshotBackingChanged", err)
		}
	})
	t.Run("replaced kernel is refused", func(t *testing.T) {
		f := newBackingFixture(ctx, t)
		snap := f.capture(ctx, t, "snap/d1/captures/c1/v2/mem")
		replaceFile(t, f.kernel, "kernel-v2")
		if err := f.m.verifySnapshotBacking(ctx, snap, f.base); !errors.Is(err, ErrSnapshotBackingChanged) {
			t.Fatalf("verify = %v, want ErrSnapshotBackingChanged", err)
		}
	})
	t.Run("capture without identity is refused", func(t *testing.T) {
		// Every snapshot taken before ADR-510 looks like this.
		f := newBackingFixture(ctx, t)
		snap := &Snapshot{FCVersion: testFCVersion, StorageKey: "snap/d1/captures/legacy/v2/mem"}
		if err := f.m.verifySnapshotBacking(ctx, snap, f.base); !errors.Is(err, ErrSnapshotBackingUnverified) {
			t.Fatalf("verify = %v, want ErrSnapshotBackingUnverified", err)
		}
	})
	t.Run("corrupt identity is refused", func(t *testing.T) {
		f := newBackingFixture(ctx, t)
		if err := f.m.storage.Put(ctx, "snap/d1/captures/c1/v2/backing", bytes.NewReader([]byte(`{"version":1}`))); err != nil {
			t.Fatal(err)
		}
		snap := &Snapshot{FCVersion: testFCVersion, StorageKey: "snap/d1/captures/c1/v2/mem"}
		if err := f.m.verifySnapshotBacking(ctx, snap, f.base); !errors.Is(err, ErrSnapshotBackingUnverified) {
			t.Fatalf("verify = %v, want ErrSnapshotBackingUnverified", err)
		}
	})
	t.Run("manager without storage keeps legacy behaviour", func(t *testing.T) {
		m := newTestManager(&fakeRunner{}, &fakeVMM{})
		if err := m.verifySnapshotBacking(ctx, usableSnapshot(), "/base.ext4"); err != nil {
			t.Fatalf("verify without storage = %v, want nil", err)
		}
	})
}

// The digest memo is keyed by (device, inode, size), never mtime: the cache's
// LRU touch rewrites mtime on every read and must not force a re-hash.
func TestFileDigestMemoIgnoresMtimeButNotReplacement(t *testing.T) {
	ctx := t.Context()
	f := newBackingFixture(ctx, t)
	first, err := f.m.fileDigest(f.base)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(f.base, time.Now().Add(time.Hour), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	again, err := f.m.fileDigest(f.base)
	if err != nil || again != first {
		t.Fatalf("digest after mtime touch = %q, %v; want memoized %q", again, err, first)
	}
	replaceFile(t, f.base, "base-layout-b")
	replaced, err := f.m.fileDigest(f.base)
	if err != nil || replaced == first {
		t.Fatalf("digest after replacement = %q, %v; want a new digest", replaced, err)
	}
}

// A refused restore must never reach vmm.Restore: loading the RAM is exactly
// what corrupts the guest. The wake cold-boots instead (schedd then marks the
// snapshot stale because the completed method is cold_boot).
func TestWakeRefusesRestoreOntoChangedBase(t *testing.T) {
	ctx := t.Context()
	f := newBackingFixture(ctx, t)
	snap := f.capture(ctx, t, "snap/d1/captures/c1/v2/mem")
	replaceFile(t, f.base, "base-layout-b")
	inst, err := f.m.Wake(ctx, WakeRequest{
		Instance: "restore-A", BaseKey: f.base, LayerKey: filepath.Join(f.dir, "layer.ext4"),
		VcpuCount: 2, MemSizeMiB: 128, Plan: api.PlanHobby, Snapshot: snap,
	})
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if inst.Method != WakeColdBoot {
		t.Fatalf("method = %s, want cold boot", inst.Method)
	}
	f.vmm.mu.Lock()
	defer f.vmm.mu.Unlock()
	if len(f.vmm.restored) != 0 || f.vmm.bootCount != 1 {
		t.Fatalf("restored=%v bootCount=%d, want no restore and one cold boot", f.vmm.restored, f.vmm.bootCount)
	}
}

func TestWakeRestoresOntoUnchangedBase(t *testing.T) {
	ctx := t.Context()
	f := newBackingFixture(ctx, t)
	snap := f.capture(ctx, t, "snap/d1/captures/c1/v2/mem")
	inst, err := f.m.Wake(ctx, WakeRequest{
		Instance: "restore-A", BaseKey: f.base, LayerKey: filepath.Join(f.dir, "layer.ext4"),
		VcpuCount: 2, MemSizeMiB: 128, Plan: api.PlanHobby, Snapshot: snap,
	})
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if inst.Method != WakeRestore {
		t.Fatalf("method = %s, want restore", inst.Method)
	}
}

// vmmd primes digests at startup so the first restore does not hash the
// base in its wake path.
func TestPrimeBackingDigestsIdentifiesKernelAndConfiguredBases(t *testing.T) {
	ctx := t.Context()
	f := newBackingFixture(ctx, t)
	f.m.WithBaseGenerations(map[string]string{f.base: "ghcr.io/example/runner@sha256:1"})
	f.m.PrimeBackingDigests(ctx)
	f.m.backingMu.Lock()
	primed := len(f.m.backingDigests)
	f.m.backingMu.Unlock()
	if primed != 2 {
		t.Fatalf("primed %d digests, want kernel and base", primed)
	}
}

func TestClassifyBackingRefusalAsSnapshotStale(t *testing.T) {
	for _, err := range []error{ErrSnapshotBackingChanged, ErrSnapshotBackingUnverified} {
		if got := ClassifyWakeError(err, WakeContext{}); got != WakeReasonSnapshotStale {
			t.Errorf("ClassifyWakeError(%v) = %q, want %q", err, got, WakeReasonSnapshotStale)
		}
	}
}

// A live-migration capture is restored on the destination through the same
// backing check as a park; it must carry the identity too (H5-12).
func TestMigrationCaptureRestoresOnTheDestination(t *testing.T) {
	ctx := t.Context()
	f := newBackingFixture(ctx, t)
	layer := filepath.Join(f.dir, "layer.ext4")
	if _, err := f.m.Wake(ctx, WakeRequest{
		Instance: "source", BaseKey: f.base, LayerKey: layer, VcpuCount: 2, MemSizeMiB: 128, Plan: api.PlanHobby,
	}); err != nil {
		t.Fatalf("Wake(source): %v", err)
	}
	memKey := "snap/d1/warm/captures/m1/v2/mem"
	if _, err := f.m.SnapshotKeepAlive(ctx, "source", SnapshotSpec{StorageKey: memKey, VMStateStorageKey: "snap/d1/warm/captures/m1/v2/vmstate"}); err != nil {
		t.Fatalf("SnapshotKeepAlive: %v", err)
	}
	inst, err := f.m.Wake(ctx, WakeRequest{
		Instance: "destination", BaseKey: f.base, LayerKey: layer, VcpuCount: 2, MemSizeMiB: 128, Plan: api.PlanHobby,
		Snapshot: &Snapshot{FCVersion: testFCVersion, StorageKey: memKey, VMStatePath: "/snap/state"},
	})
	if err != nil {
		t.Fatalf("Wake(destination): %v", err)
	}
	if inst.Method != WakeRestore {
		t.Fatalf("destination method = %s, want restore from the migration capture", inst.Method)
	}
}
