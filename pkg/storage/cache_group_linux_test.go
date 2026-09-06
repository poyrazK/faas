//go:build linux

package storage_test

import (
	"github.com/onebox-faas/faas/pkg/storage"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestLocalCacheSharedGroupInheritance(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cache")
	if err := os.MkdirAll(root, 0o770); err != nil {
		t.Fatal(err)
	}
	// On the metal host use a group different from the root writer's group.
	// Unprivileged CI still verifies all setgid modes and its inherited group.
	if os.Geteuid() == 0 {
		if err := os.Chown(root, -1, 65534); err != nil {
			t.Fatal(err)
		}
	}
	rootInfo, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	wantGID := rootInfo.Sys().(*syscall.Stat_t).Gid
	parent := newFakeBackend()
	cache, err := storage.NewLocalCacheBackend(parent, root, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	check := func(path string) {
		t.Helper()
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Sys().(*syscall.Stat_t).Gid; got != wantGID {
			t.Fatalf("%s gid=%d want=%d", path, got, wantGID)
		}
		if info.Mode()&os.ModeSetgid == 0 || info.Mode().Perm() != 0o770 {
			t.Fatalf("%s mode=%v: need group write and group inheritance", path, info.Mode())
		}
	}
	check(root)
	// Constructor restart, Put spool, and Get materialization used to clear setgid.
	if _, err = storage.NewLocalCacheBackend(parent, root, 1<<20); err != nil {
		t.Fatal(err)
	}
	check(root)
	if err = cache.Put(t.Context(), "put-key", strings.NewReader("put")); err != nil {
		t.Fatal(err)
	}
	parent.blobs["get-key"] = []byte("get")
	rc, err := cache.Get(t.Context(), "get-key")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.Copy(io.Discard, rc); err != nil {
		t.Fatal(err)
	}
	_ = rc.Close()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	buckets := 0
	for _, entry := range entries {
		if entry.IsDir() {
			buckets++
			check(filepath.Join(root, entry.Name()))
		}
	}
	if buckets != 2 {
		t.Fatalf("buckets=%d want two independently populated buckets", buckets)
	}
}
