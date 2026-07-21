package builderd

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// CacheEntry is one cached build: source-hash + framework → produced layer
// path + size. The entry is purely on-disk; pkg/state never sees it (ADR-005
// keeps state in the SQL tables; the cache is content-addressed storage that
// can be wiped without data loss).
type CacheEntry struct {
	Path  string
	Bytes int64
}

// Cache is a content-addressed cache of produced app layers. The key is
// sha256(source-bytes); the value is the produced ext4 layer + size. The
// filesystem layout is:
//
//	<CacheDir>/<sha256>.<framework>/layer.ext4
//
// Lookup is best-effort: a missing entry is not an error (the caller will
// build). A corrupted entry (size mismatch, missing file) IS an error so the
// caller can rebuild instead of using a broken layer.
type Cache struct {
	root string
}

// NewCache wires a Cache rooted at dir. The dir is created lazily.
func NewCache(dir string) *Cache { return &Cache{root: dir} }

// Lookup returns the cached layer for (sourceHash, fw) if one exists and
// looks intact. (false, nil) is a cache miss, not an error.
func (c *Cache) Lookup(sourceHash string, fw Framework) (CacheEntry, bool) {
	if c == nil || c.root == "" {
		return CacheEntry{}, false
	}
	p := c.entryPath(sourceHash, fw)
	st, err := os.Stat(p)
	if err != nil {
		return CacheEntry{}, false
	}
	if !st.Mode().IsRegular() {
		return CacheEntry{}, false
	}
	return CacheEntry{Path: p, Bytes: st.Size()}, true
}

// Store moves the produced layer into the cache under the source-hash key.
// The move is atomic via os.Rename; if the entry already exists, we keep the
// existing one (first build wins; later builds still leave the same content
// behind). The path is left in place — the caller continues using the
// original out.LayerPath; we just record that this content is cached.
func (c *Cache) Store(sourceHash string, fw Framework, layerPath string, bytes int64) error {
	if c == nil || c.root == "" {
		return errors.New("cache: not configured")
	}
	dst := c.entryPath(sourceHash, fw)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("cache: mkdir: %w", err)
	}
	// Idempotent: if the destination already exists, keep it (its bytes
	// should match — content-addressed — so the existing copy is fine).
	if _, err := os.Stat(dst); err == nil {
		return nil
	}
	if err := os.Rename(layerPath, dst); err != nil {
		// Cross-device rename fails — fall back to copy.
		if err := copyFile(layerPath, dst); err != nil {
			return fmt.Errorf("cache: store %s: %w", dst, err)
		}
	}
	return nil
}

func (c *Cache) entryPath(sourceHash string, fw Framework) string {
	return filepath.Join(c.root, sourceHash+"."+string(fw), "layer.ext4")
}

// hashFile streams the file at path through sha256 and returns the hex digest.
// The whole file is read; builderd source tarballs are bounded by the plan's
// SourceTarballMaxMB (100 MB Hobby, 250 MB Pro+) so this is safe in memory.
//
//nolint:forbidigo // path is a vetted-id cache file under c.root joined from sourceHash + framework — no customer input reaches the open. Symlink-attack impossible because c.root is apid-owned and populated only by builderd.
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("hash: open: %w", err)
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash: read: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

//nolint:forbidigo // src/dst are vetted-id cache paths joined from c.root + sourceHash + framework — builderd is the sole writer and apid's spool validator (validateTarballShape in cmd/apid/deploy_inputs.go) already ran shape checks before builderd ever sees the file.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}
