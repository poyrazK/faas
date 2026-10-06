// adr: 474
package fcvm

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Production rc.236: the artifact cache renamed a refreshed layer over a path
// that a running VM still had bound, and the next cold boot of the same app
// failed with "bind source replaced while referenced". The refreshed file is a
// new bound source; each inode keeps and restores its own mode.
func TestResourceAssetsBindSourceReplacedWhileReferenced(t *testing.T) {
	v := NewJailerVMM(t.TempDir(), time.Second)
	path := filepath.Join(t.TempDir(), "layer.ext4")
	bind := func() (resourceFileIdentity, *os.File) {
		t.Helper()
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		info, _ := f.Stat()
		identity, _ := resourceFileID(info)
		v.mu.Lock()
		handle, err := v.retainBindSourceLocked(bindSourceKey{path, identity}, info.Mode().Perm(), f)
		v.mu.Unlock()
		if err != nil {
			t.Fatalf("bind %s: %v", path, err)
		}
		if err := chmodResourceFile(handle, identity, 0o644); err != nil {
			t.Fatal(err)
		}
		return identity, handle
	}
	if err := os.WriteFile(path, []byte("v1"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldIdentity, oldHandle := bind()
	refreshed := path + ".tmp"
	if err := os.WriteFile(refreshed, []byte("v2"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(refreshed, path); err != nil {
		t.Fatal(err)
	}
	newIdentity, _ := bind()
	if oldIdentity == newIdentity {
		t.Fatal("fixture did not replace the inode")
	}
	if got := v.bindSourceRefs(path); got != 2 {
		t.Fatalf("refs on %s = %d, want one per inode (2)", path, got)
	}
	if err := v.releaseBindSource(path, newIdentity); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("refreshed file mode = %v, want restored 0600", info.Mode().Perm())
	}
	// The released handle is closed; stat the replaced inode before release.
	if info, _ := oldHandle.Stat(); info.Mode().Perm() != 0o644 {
		t.Fatalf("replaced inode lost its bound mode early: %v", info.Mode().Perm())
	}
	if err := v.releaseBindSource(path, oldIdentity); err != nil {
		t.Fatal(err)
	}
	if len(v.bindSourceModes) != 0 {
		t.Fatalf("bind sources left tracked: %d", len(v.bindSourceModes))
	}
}
