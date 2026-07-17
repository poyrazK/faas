package builderd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCache_MissReturnsFalse(t *testing.T) {
	c := NewCache(t.TempDir())
	if _, ok := c.Lookup("deadbeef", FrameworkNode); ok {
		t.Error("lookup on empty cache should miss")
	}
}

func TestCache_StoreAndLookup(t *testing.T) {
	root := t.TempDir()
	c := NewCache(root)
	// Create a fake layer file.
	src := filepath.Join(t.TempDir(), "layer.ext4")
	if err := os.WriteFile(src, []byte("fake layer bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	hash := "0123456789abcdef"
	if err := c.Store(hash, FrameworkNode, src, 16); err != nil {
		t.Fatal(err)
	}
	got, ok := c.Lookup(hash, FrameworkNode)
	if !ok {
		t.Fatal("expected cache hit")
	}
	if got.Bytes != 16 {
		t.Errorf("bytes = %d, want 16", got.Bytes)
	}
	if _, err := os.Stat(got.Path); err != nil {
		t.Errorf("cache file missing: %v", err)
	}
}

func TestCache_StoreIdempotent(t *testing.T) {
	root := t.TempDir()
	c := NewCache(root)
	src := filepath.Join(t.TempDir(), "layer.ext4")
	if err := os.WriteFile(src, []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.Store("h1", FrameworkPython, src, 5); err != nil {
		t.Fatal(err)
	}
	// Second store with different src — should NOT overwrite.
	src2 := filepath.Join(t.TempDir(), "layer2.ext4")
	if err := os.WriteFile(src2, []byte("second"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.Store("h1", FrameworkPython, src2, 6); err != nil {
		t.Fatal(err)
	}
	got, ok := c.Lookup("h1", FrameworkPython)
	if !ok {
		t.Fatal("expected cache hit")
	}
	if got.Bytes != 5 {
		t.Errorf("bytes = %d, want 5 (first writer wins)", got.Bytes)
	}
}

func TestCache_NilSafe(t *testing.T) {
	var c *Cache
	if _, ok := c.Lookup("h", FrameworkNode); ok {
		t.Error("nil cache should miss")
	}
	if err := c.Store("h", FrameworkNode, "/x", 1); err == nil {
		t.Error("nil cache Store should error")
	}
}

func TestHashFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := hashFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// sha256("hello") = 2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824
	const want = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if got != want {
		t.Errorf("hash = %s, want %s", got, want)
	}
}

func TestHashFile_Missing(t *testing.T) {
	if _, err := hashFile("/no/such/file"); err == nil {
		t.Error("expected error on missing file")
	}
}
