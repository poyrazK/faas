//go:build linux

// adr: 796 — these are read-only host checks, not native VM acceptance.
package fcvm

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

func TestNativeSnapshotMemoryRefusesOrdinaryAndRedirectedKernelRoots(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "kernel")
	if err := os.Symlink("/sys/fs/cgroup", link); err != nil {
		t.Fatal(err)
	}
	owner := nativeLaunchRecord{Lease: qualificationLease(uuid.NewString()), Authorized: true, PID: 42, StartTime: 101}
	for _, path := range []string{root, link, "relative", root + "/"} {
		backend := linuxNativeSnapshotMemory{root: path}
		if err := backend.CheckSupport(); err == nil {
			t.Fatal("non-kernel root supplied memory authority", path)
		}
		if fence, err := backend.Open(t.Context(), owner); err == nil || fence != nil {
			t.Fatal("non-kernel scope supplied mutation capability", path, err)
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 {
		t.Fatal("memory preflight created host controls", err)
	}
}

func TestNativeSnapshotMemoryRejectsUnrepresentableLimitsBeforeKernelAccess(t *testing.T) {
	for _, ram := range []int{0, -1, int(^uint(0) >> 1), int(^uint(0)>>1) - 100, int(^uint64(0) >> 20)} {
		owner := nativeLaunchRecord{Lease: qualificationLease(uuid.NewString()), Authorized: true, PID: 42, StartTime: 101}
		owner.Lease.MemoryMaxMiB = ram
		backend := linuxNativeSnapshotMemory{root: "/missing-kernel-root"}
		if fence, err := backend.Open(t.Context(), owner); err == nil || fence != nil || os.IsNotExist(err) {
			t.Fatal("invalid limits reached kernel root", ram, err)
		}
	}
}

func TestNativeSnapshotPlacementRequiresOriginalProtectedDisk(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	file, err := os.CreateTemp(root, "original-input")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var filesystem unix.Statfs_t
	if err := unix.Fstatfs(int(file.Fd()), &filesystem); err != nil {
		t.Fatal(err)
	}
	backend := linuxNativeImageSources{}
	err = backend.CheckSnapshotDirectory(t.Context(), file, root)
	if nativeCloneFilesystemSupported(filesystem.Type) && err != nil || !nativeCloneFilesystemSupported(filesystem.Type) && err == nil {
		t.Fatal("placement did not reflect actual filesystem", err)
	}
	link := filepath.Join(root, "redirected")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{link, "/", "relative", root + "/"} {
		if err := backend.CheckSnapshotDirectory(t.Context(), file, path); err == nil {
			t.Fatal("redirected placement accepted", path)
		}
	}
	if err := os.Chmod(root, 0o770); err != nil {
		t.Fatal(err)
	}
	if err := backend.CheckSnapshotDirectory(t.Context(), file, root); err == nil {
		t.Fatal("writable shared output directory accepted")
	}
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := backend.CheckSnapshotDirectory(canceled, file, root); err == nil {
		t.Fatal("canceled placement succeeded")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 2 {
		t.Fatal("placement preflight created capture outputs", err)
	}
}
