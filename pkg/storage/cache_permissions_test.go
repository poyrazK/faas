package storage

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// TestEnsureSharedCacheDirFallsBackWithoutSetgid models the production apid
// sandbox: RestrictSUIDSGID rejects chmod when the requested mode contains the
// setgid bit. The owner must still repair group write so sibling faas daemons
// can populate the same cache bucket.
func TestEnsureSharedCacheDirFallsBackWithoutSetgid(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "bucket")
	chmod := func(path string, mode os.FileMode) error {
		if mode&os.ModeSetgid != 0 {
			return syscall.EPERM
		}
		return os.Chmod(path, mode)
	}
	if err := ensureSharedCacheDirWith(dir, os.MkdirAll, chmod, os.Stat); err != nil {
		t.Fatalf("ensureSharedCacheDirWith: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o770 {
		t.Fatalf("mode = %04o, want 0770", got)
	}
}

// TestEnsureSharedCacheDirAcceptsWritableCrossOwnerBucket models a shard that
// another faas daemon owns. chmod returns EPERM, but the already-correct group
// permissions make it safe to share and must not prevent daemon startup.
func TestEnsureSharedCacheDirAcceptsWritableCrossOwnerBucket(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "bucket")
	if err := os.Mkdir(dir, 0o770); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o770); err != nil {
		t.Fatal(err)
	}
	chmod := func(string, os.FileMode) error { return syscall.EPERM }
	if err := ensureSharedCacheDirWith(dir, os.MkdirAll, chmod, os.Stat); err != nil {
		t.Fatalf("ensureSharedCacheDirWith: %v", err)
	}
}
