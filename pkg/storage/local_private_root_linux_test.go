//go:build linux

// adr: 20261022184951 — native runtime snapshots live in a tree only vmmd writes.
package storage

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckPrivateExclusiveRootRefusesSharedOrOpenTrees(t *testing.T) {
	backend := exclusiveLocalFixture(t)
	for _, tc := range []struct {
		name string
		mode os.FileMode
		ok   bool
	}{
		{name: "owner only", mode: 0o700, ok: true},
		{name: "group read", mode: 0o750 | os.ModeSetgid, ok: true},
		{name: "group write", mode: 0o770 | os.ModeSetgid},
		{name: "others read", mode: 0o755},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.Chmod(backend.root, tc.mode); err != nil {
				t.Fatal(err)
			}
			if err := CheckPrivateExclusiveRoot(backend.root); (err == nil) != tc.ok {
				t.Fatalf("CheckPrivateExclusiveRoot(%v) = %v, want ok %v", tc.mode, err, tc.ok)
			}
		})
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(backend.root, link); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(backend.root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := CheckPrivateExclusiveRoot(link); err == nil {
		t.Fatal("a symlinked root was accepted")
	}
}

func TestDeleteExclusiveRemovesOnlyTheObjectAndItsOwnDirectories(t *testing.T) {
	backend := exclusiveLocalFixture(t)
	ctx := t.Context()
	const keep = "snap/dep/captures"
	victim, sibling := keep+"/a/v2/mem", keep+"/b/v2/mem"
	for _, key := range []string{victim, keep + "/a/v2/vmstate", sibling} {
		if _, err := backend.PutExclusiveArtifact(ctx, key, bytes.NewReader([]byte(key)), int64(len(key))); err != nil {
			t.Fatal(err)
		}
	}
	if err := backend.DeleteExclusive(ctx, victim, keep); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(backend.root, victim)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("deleted object is still present", err)
	}
	// The capture directory still holds vmstate, so it stays.
	if _, err := os.Lstat(filepath.Join(backend.root, keep, "a", "v2", "vmstate")); err != nil {
		t.Fatal(err)
	}
	if err := backend.DeleteExclusive(ctx, keep+"/a/v2/vmstate", keep); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(backend.root, keep, "a")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the emptied capture directory was kept", err)
	}
	if _, err := os.Lstat(filepath.Join(backend.root, sibling)); err != nil {
		t.Fatal("a sibling capture was removed", err)
	}
	if err := backend.DeleteExclusive(ctx, victim, keep); err != nil {
		t.Fatal("a missing object must be an idempotent success", err)
	}
	if err := backend.DeleteExclusive(ctx, keep+"/missing/v2/mem", keep); err != nil {
		t.Fatal("a missing capture directory must be an idempotent success", err)
	}
	if err := backend.DeleteExclusive(ctx, sibling, "snap/other"); !errors.Is(err, ErrInvalidKey) {
		t.Fatal("a keep directory outside the key was accepted", err)
	}
	if err := os.Chmod(filepath.Join(backend.root, "snap", "dep"), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := backend.DeleteExclusive(ctx, sibling, keep); err == nil {
		t.Fatal("a tree another principal can write was trusted")
	}
}

func TestDeleteExclusiveNeverFollowsALinkedDirectory(t *testing.T) {
	backend := exclusiveLocalFixture(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "mem"), []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(backend.root, "snap", "dep", "captures", "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(backend.root, "snap", "dep", "captures", "a", "v2")); err != nil {
		t.Fatal(err)
	}
	if err := backend.DeleteExclusive(t.Context(), "snap/dep/captures/a/v2/mem", "snap/dep/captures"); err == nil {
		t.Fatal("a symlinked directory was followed")
	}
	if _, err := os.Lstat(filepath.Join(outside, "mem")); err != nil {
		t.Fatal("an object outside the tree was removed", err)
	}
}
