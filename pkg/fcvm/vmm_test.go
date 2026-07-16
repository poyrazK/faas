package fcvm

import (
	"os"
	"path/filepath"
	"testing"
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
