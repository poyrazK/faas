//go:build linux

package fcvm

// adr: 595

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"golang.org/x/sys/unix"
)

// A real Linux mmap verifies procfs identity and COW semantics without KVM.
// This test does not certify Firecracker restore, CPU state or guest readiness.
func TestSnapshotMemoryObserverOwnPrivateMapping(t *testing.T) {
	bytes := 4 * os.Getpagesize()
	path := filepath.Join(t.TempDir(), "memory")
	if err := os.WriteFile(path, make([]byte, bytes), 0o444); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	pin := pinnedRuntimeDrive{file: file, info: info, observation: RuntimeDriveObservation{Source: runtimeadmission.ArtifactSource{Bytes: int64(bytes)}}}
	if _, _, err := observeSnapshotMemoryMaps(t.Context(), "/proc", os.Getpid(), os.Getuid(), pin); err == nil {
		t.Fatal("an open file descriptor substituted for a memory mapping")
	}
	memory, err := unix.Mmap(int(file.Fd()), 0, bytes, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_PRIVATE)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if memory != nil {
			_ = unix.Munmap(memory)
		}
	})
	// Firecracker may close the descriptor after mmap. Retain the observer's
	// separate pin, while proving that the original mmap descriptor is gone.
	pinFile, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pinFile.Close() })
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	pin.file = pinFile
	memory[0], memory[bytes-1] = 7, 9 // Fault private pages without changing backing.
	start, ranges, err := observeSnapshotMemoryMaps(t.Context(), "/proc", os.Getpid(), os.Getuid(), pin)
	if err != nil || start == "" || len(ranges) != 1 || ranges[0].Offset != 0 || ranges[0].End-ranges[0].Start != int64(bytes) {
		t.Fatalf("real private mapping: start=%q ranges=%+v error=%v", start, ranges, err)
	}
	body, err := os.ReadFile(path)
	if err != nil || body[0] != 0 || body[len(body)-1] != 0 {
		t.Fatal("private mapping changed captured memory", err)
	}
	if _, _, err := observeSnapshotMemoryMaps(t.Context(), "/proc", os.Getpid(), os.Getuid()+1, pin); err == nil {
		t.Fatal("another jail UID authorized this process")
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := observeSnapshotMemoryMaps(canceled, "/proc", os.Getpid(), os.Getuid(), pin); err == nil {
		t.Fatal("cancellation produced a mapping observation")
	}
	if err := unix.Munmap(memory); err != nil {
		t.Fatal(err)
	}
	memory = nil
	if _, _, err := observeSnapshotMemoryMaps(t.Context(), "/proc", os.Getpid(), os.Getuid(), pin); err == nil {
		t.Fatal("unmapped memory retained a usable observation")
	}
	shared, err := unix.Mmap(int(pinFile.Fd()), 0, bytes, unix.PROT_READ, unix.MAP_SHARED)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unix.Munmap(shared) }()
	if _, _, err := observeSnapshotMemoryMaps(t.Context(), "/proc", os.Getpid(), os.Getuid(), pin); err == nil {
		t.Fatal("shared memory authorized a private snapshot restore")
	}
}
