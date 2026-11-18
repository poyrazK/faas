package fcvm

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// A builder's BuildKit seed drive goes back to the owner of builderd's drive
// directory once the VM is gone, and only a recorded drive is touched.
func TestHandBackBuildKitSeed(t *testing.T) {
	dir := t.TempDir()
	seed := filepath.Join(dir, "build-seed-buildkit.ext4")
	if err := os.WriteFile(seed, []byte("seed"), 0o600); err != nil {
		t.Fatal(err)
	}
	v := &JailerVMM{}
	v.handBackBuildKitSeed("unknown") // no recorded drive: nothing to do
	v.noteBuildKitSeedSource("builder-1", seed)
	v.handBackBuildKitSeed("builder-1")
	if _, still := v.buildkitSeedSources["builder-1"]; still {
		t.Fatal("handed-back seed drive is still recorded")
	}
	info, err := os.Stat(seed)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, want := info.Sys().(*syscall.Stat_t), parent.Sys().(*syscall.Stat_t)
	if got.Uid != want.Uid || got.Gid != want.Gid {
		t.Fatalf("seed owned by %d:%d, want the directory's %d:%d", got.Uid, got.Gid, want.Uid, want.Gid)
	}
}

func TestHandBackRefusesANonRegularFile(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "seed.ext4")
	if err := os.Symlink("/etc/passwd", link); err != nil {
		t.Fatal(err)
	}
	if err := handBackToDirectoryOwner(link); err == nil {
		t.Fatal("handed back a symlink")
	}
	if err := handBackToDirectoryOwner(filepath.Join(dir, "missing")); !os.IsNotExist(err) {
		t.Fatalf("missing drive = %v, want not-exist", err)
	}
}
