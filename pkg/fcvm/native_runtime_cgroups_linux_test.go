//go:build linux

package fcvm

// adr: 769
import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestNativeRuntimeCgroupRefusesOrdinaryFilesAndSymlinkRoots(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if file, err := openNativeRuntimeCgroupRoot(root); err == nil {
		_ = file.Close()
		t.Fatal("ordinary filesystem supplied kernel limits")
	}
	link := filepath.Join(root, "kernel")
	if err := os.Symlink("/sys/fs/cgroup", link); err != nil {
		t.Fatal(err)
	}
	if file, err := openNativeRuntimeCgroupRoot(link); err == nil {
		_ = file.Close()
		t.Fatal("symlink supplied pinned cgroup root")
	}
	for _, path := range []string{"relative", root + "/../" + filepath.Base(root), root + "/"} {
		if file, err := openNativeRuntimeCgroupRoot(path); err == nil {
			_ = file.Close()
			t.Fatal("noncanonical root accepted", path)
		}
	}
}

func TestNativeRuntimeCgroupChecksActualKernelMountIdentity(t *testing.T) {
	root, err := openNativeRuntimeCgroupRoot("/sys/fs/cgroup")
	if err != nil {
		t.Skipf("read-only protected cgroup v2 root unavailable: %v", err)
	}
	defer root.Close()
	fd := int(root.Fd())
	mount, err := nativeRuntimeCgroupMountID(fd)
	if err != nil || mount == 0 {
		t.Fatal("kernel mount identity unavailable", mount, err)
	}
	file, err := nativeHelperCgroupControl(fd, "cgroup.procs", unix.O_RDONLY)
	if err != nil {
		t.Fatal(err)
	}
	controlMount, mountErr := nativeRuntimeCgroupMountID(int(file.Fd()))
	closeErr := file.Close()
	if mountErr != nil || closeErr != nil || controlMount != mount {
		t.Fatal("control identity differs from pinned kernel directory", controlMount, mountErr, closeErr)
	}
	if _, err := readNativeRuntimeControl(fd, "cgroup.procs"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"", ".", "..", "../other", "/absolute", "child/../other", "child\\scope"} {
		if child, err := openNativeRuntimeCgroupPath(fd, path); err == nil {
			_ = unix.Close(child)
			t.Fatal("cgroup traversal accepted", path)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := walkNativeRuntimeCgroups(ctx, fd, "unused", true); err == nil {
		t.Fatal("cancelled walker read host cgroups")
	}
}

func TestNativeRuntimeCgroupControlRefusesNonKernelData(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "memory.max"), []byte("max\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	if _, err := readNativeRuntimeControl(int(dir.Fd()), "memory.max"); err == nil {
		t.Fatal("regular file qualified as a kernel limit")
	}
}
