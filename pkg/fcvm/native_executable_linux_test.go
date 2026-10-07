//go:build linux

package fcvm

// adr: 651
import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// Actual procfs and pidfd diagnostics; this executes no Firecracker/KVM guest.
func TestNativeExecutableLinuxRetainsActualProcfsInputWithLivePidfd(t *testing.T) {
	j, lease, _, _ := preparedNativeRegionalResources(t)
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	// Go's build directory can follow a group-writable test-runner umask.
	// Select a private owned copy of the same actual ELF bytes as the source.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	path = nativeExecutableFixtureFile(t, "selected-test-elf", raw)
	if err := j.captureExecutable(t.Context(), lease, path); err != nil {
		t.Fatal(err)
	}
	owner, err := j.read(lease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	source, err := j.readExecutable(owner)
	if err != nil || source == nil {
		t.Fatal(source, err)
	}
	handle, err := openNativeProcess(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := handle.Close(); err != nil {
			t.Error(err)
		}
	})
	live, ok := handle.(nativeLiveProcessHandle)
	if !ok {
		t.Fatal("pidfd has no liveness observation")
	}
	if err := live.CheckLive(); err != nil {
		t.Fatal(err)
	}
	//nolint:forbidigo // Actual kernel link of this test's pinned task.
	f, err := os.Open("/proc/self/exe")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.Close(); err != nil {
			t.Error(err)
		}
	})
	size, digest, before, err := nativeExecutableDigest(t.Context(), f)
	if err != nil || size != source.Bytes || digest != source.SHA256 {
		t.Fatal("procfs bytes differ from selected source", err)
	}
	after, err := snapshotFileStamp(f)
	if err := errors.Join(err, live.CheckLive()); err != nil || before != after {
		t.Fatal("pinned executable changed", err)
	}
	opened, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	current, err := os.Stat("/proc/self/exe")
	if err != nil || !os.SameFile(opened, current) {
		t.Fatal("procfs executable replaced", err)
	}
}
