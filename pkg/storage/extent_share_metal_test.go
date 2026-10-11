//go:build metal && linux

// adr: 633
package storage

import (
	"bytes"
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
)

// metalReflinkDir returns a directory on the node's reflink-capable artifact
// filesystem (XFS reflink=1 under /srv/fc in production).
func metalReflinkDir(t *testing.T) string {
	t.Helper()
	root := os.Getenv("FAAS_METAL_REFLINK_DIR")
	if root == "" {
		root = "/srv/fc"
	}
	dir, err := os.MkdirTemp(root, ".adr633-metal-")
	if err != nil {
		t.Skipf("no writable reflink filesystem at %s: %v", root, err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// TestMetalSnapshotDriveSharesItsLayer walks the production shape on XFS:
// a cached app layer, a writable instance clone the guest writes one block
// of, the capture's frozen clone published through the cache, and a replica
// that fetched the drive whole and hands its unchanged blocks back.
func TestMetalSnapshotDriveSharesItsLayer(t *testing.T) {
	const size = 8 << 20
	const block = 4096
	dir := metalReflinkDir(t)
	ctx := context.Background()
	parent := newBudgetParent()
	cache, err := NewLocalCacheBackend(parent, filepath.Join(dir, "cache"), 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	layer := make([]byte, size)
	if _, err := rand.Read(layer); err != nil {
		t.Fatal(err)
	}
	const layerKey = "apps/dep.ext4"
	if err := cache.Put(ctx, layerKey, bytes.NewReader(layer)); err != nil {
		t.Fatal(err)
	}
	layerPath, _, _ := cache.LocalPath(layerKey)

	// The instance's writable drive is a clone of the cached layer.
	instance, err := os.Create(filepath.Join(dir, "instance.ext4"))
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.Open(layerPath)
	if err != nil {
		t.Fatal(err)
	}
	if !cloneInto(instance, src) {
		t.Skip("filesystem does not support FICLONE")
	}
	_ = src.Close()
	if _, err := instance.WriteAt(bytes.Repeat([]byte{0xAB}, block), 3*block); err != nil {
		t.Fatal(err)
	}
	if err := instance.Close(); err != nil {
		t.Fatal(err)
	}
	if got, ok := ExclusiveBytes(filepath.Join(dir, "instance.ext4")); !ok || got != block {
		t.Fatalf("guest-written bytes = %d (ok=%v), want %d", got, ok, block)
	}

	// Capture: the frozen drive is published from a file and cloned.
	const driveKey = "snap/dep/captures/c1/v2/drive"
	frozen, err := os.Open(filepath.Join(dir, "instance.ext4"))
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.Put(ctx, driveKey, frozen); err != nil {
		t.Fatal(err)
	}
	_ = frozen.Close()
	if err := os.Remove(filepath.Join(dir, "instance.ext4")); err != nil {
		t.Fatal(err)
	}
	drivePath, _, _ := cache.LocalPath(driveKey)
	if got, want := physical(t, layerPath, drivePath), int64(size+block); got != want {
		t.Fatalf("capture footprint = %d, want %d (layer + one written block)", got, want)
	}

	// Replica: the drive arrives from the parent as a full copy.
	const replicaKey = "snap/dep/captures/c2/v2/drive"
	drive, err := os.ReadFile(drivePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.Put(ctx, replicaKey, bytes.NewReader(drive)); err != nil {
		t.Fatal(err)
	}
	replicaPath, _, _ := cache.LocalPath(replicaKey)
	if got, want := physical(t, layerPath, replicaPath), int64(2*size); got != want {
		t.Fatalf("dense replica footprint = %d, want %d", got, want)
	}
	shared, err := ShareUnchangedBlocks(ctx, cache, replicaKey, layerKey)
	if err != nil {
		t.Fatal(err)
	}
	if shared != size-block {
		t.Fatalf("shared = %d, want %d", shared, size-block)
	}
	if got, want := physical(t, layerPath, replicaPath), int64(size+block); got != want {
		t.Fatalf("replica footprint after sharing = %d, want %d", got, want)
	}
	after, err := os.ReadFile(replicaPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, drive) {
		t.Fatal("sharing changed the replica's bytes")
	}
}

func physical(t *testing.T, paths ...string) int64 {
	t.Helper()
	entries := make([]cacheEntry, 0, len(paths))
	for _, p := range paths {
		entries = append(entries, cacheEntry{path: p})
	}
	return newCacheFootprint(entries).total()
}
