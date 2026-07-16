package fcvm

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Compile-time proof the production VMM satisfies the interface the Manager uses.
var _ VMM = (*JailerVMM)(nil)

func TestProvisionRewritesPathsIntoChroot(t *testing.T) {
	// provision hardlinks images into the chroot and rewrites config paths to
	// their in-chroot basenames — the jailed firecracker sees only these.
	root := t.TempDir()
	srcDir := t.TempDir()
	kernel := filepath.Join(srcDir, "vmlinux-6.1")
	base := filepath.Join(srcDir, "runner-node22.ext4")
	layer := filepath.Join(srcDir, "layer-1.ext4")
	for _, f := range []string{kernel, base, layer} {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cfg := BuildColdBootConfig(ColdBootSpec{
		KernelPath: kernel, BasePath: base, LayerPath: layer,
		VcpuCount: 2, MemSizeMiB: 128, Tap: "tap0",
	})

	v := NewJailerVMM(t.TempDir(), 0)
	out, err := v.provision(root, cfg)
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if out.BootSource.KernelImagePath != "vmlinux-6.1" {
		t.Errorf("kernel path = %q, want in-chroot basename", out.BootSource.KernelImagePath)
	}
	if out.Drives[0].PathOnHost != "runner-node22.ext4" || out.Drives[1].PathOnHost != "layer-1.ext4" {
		t.Errorf("drive paths not rewritten: %q, %q", out.Drives[0].PathOnHost, out.Drives[1].PathOnHost)
	}
	// Files must actually exist in the chroot root now.
	for _, name := range []string{"vmlinux-6.1", "runner-node22.ext4", "layer-1.ext4"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Errorf("expected %s provisioned into chroot: %v", name, err)
		}
	}
	// The original config is untouched (we returned a copy).
	if cfg.BootSource.KernelImagePath != kernel {
		t.Error("provision mutated the input config")
	}
}

func TestCopyFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "sub", "dst")
	if err := os.WriteFile(src, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(src, dst); err != nil {
		t.Fatalf("copyFile: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != "hello" {
		t.Fatalf("copied content = %q, err=%v", got, err)
	}
}

func TestLinkInto_Hardlink(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "mem.bin")
	if err := os.WriteFile(src, []byte("contents"), 0o644); err != nil {
		t.Fatal(err)
	}
	dstDir := filepath.Join(dir, "dest")
	if err := os.MkdirAll(dstDir, 0o750); err != nil {
		t.Fatal(err)
	}
	name, err := linkInto(dstDir, src)
	if err != nil {
		t.Fatalf("linkInto: %v", err)
	}
	if name != "mem.bin" {
		t.Errorf("returned name = %q, want mem.bin", name)
	}
	a, _ := os.Stat(src)
	b, _ := os.Stat(filepath.Join(dstDir, "mem.bin"))
	if !os.SameFile(a, b) {
		t.Error("expected hardlink (same inode)")
	}
}

func TestLinkInto_OverwritesExisting(t *testing.T) {
	// If dst already exists, linkInto must remove it before hardlinking.
	dir := t.TempDir()
	src := filepath.Join(dir, "f")
	if err := os.WriteFile(src, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	dstDir := filepath.Join(dir, "dst")
	if err := os.MkdirAll(dstDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dstDir, "f"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	name, err := linkInto(dstDir, src)
	if err != nil {
		t.Fatalf("linkInto: %v", err)
	}
	if name != "f" {
		t.Errorf("name = %q", name)
	}
	got, err := os.ReadFile(filepath.Join(dstDir, "f"))
	if err != nil || string(got) != "new" {
		t.Errorf("after overwrite: got %q err=%v", got, err)
	}
}

func TestMoveOut_Rename(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "snap")
	if err := os.WriteFile(src, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "sub", "snap")
	size, err := moveOut(src, dst)
	if err != nil {
		t.Fatalf("moveOut: %v", err)
	}
	if size != int64(len("payload")) {
		t.Errorf("size = %d, want %d", size, len("payload"))
	}
	if _, err := os.Stat(src); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("src should be gone after moveOut, stat err=%v", err)
	}
}

func TestMoveOut_CrossDeviceFallback(t *testing.T) {
	// /tmp and the temp dir are usually the same fs, but we can simulate the
	// fallback by removing the parent of dst so MkdirAll has to create it
	// (rename should still work; this is the happy rename branch — the
	// cross-device fallback is exercised by integration tests).
	dir := t.TempDir()
	src := filepath.Join(dir, "snap")
	if err := os.WriteFile(src, []byte("xx"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "new", "snap")
	size, err := moveOut(src, dst)
	if err != nil || size != 2 {
		t.Fatalf("moveOut happy: size=%d err=%v", size, err)
	}
}

func TestChrootRoot_AndSocketPath(t *testing.T) {
	v := NewJailerVMM("/srv/fc/jail", 30*time.Second)
	got := v.chrootRoot("inst-1")
	if !strings.HasPrefix(got, "/srv/fc/jail") {
		t.Errorf("chrootRoot = %q, want under /srv/fc/jail", got)
	}
	if !strings.HasSuffix(got, "/root") {
		t.Errorf("chrootRoot = %q, want suffix /root", got)
	}
	sock := v.socketPath("inst-1")
	if !strings.HasSuffix(sock, APISockName) {
		t.Errorf("socketPath = %q, want suffix %q", sock, APISockName)
	}
	if !strings.Contains(sock, "inst-1") {
		t.Errorf("socketPath = %q, want contains inst-1", sock)
	}
}

func TestDetectFirecrackerVersion_MissingBinary(t *testing.T) {
	// Set PATH to an empty dir so the real firecracker binary is invisible.
	t.Setenv("PATH", t.TempDir())
	_, err := DetectFirecrackerVersion(context.Background())
	if err == nil {
		t.Fatal("DetectFirecrackerVersion should fail when binary missing")
	}
}

// stubFC is a tiny script that pretends to be firecracker and prints a
// fixed version line. Used when the real binary is unavailable on CI.
func TestDetectFirecrackerVersion_WithStub(t *testing.T) {
	if _, err := exec.LookPath("firecracker"); err == nil {
		t.Skip("real firecracker present; stub test not needed")
	}
	binDir := t.TempDir()
	stub := filepath.Join(binDir, "firecracker")
	script := "#!/bin/sh\necho 'Firecracker v9.9.9-rc1'\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	v, err := DetectFirecrackerVersion(context.Background())
	if err != nil {
		t.Fatalf("DetectFirecrackerVersion: %v", err)
	}
	if v != "9.9.9-rc1" {
		t.Errorf("version = %q, want 9.9.9-rc1", v)
	}
}
