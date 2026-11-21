package storage

// adr: 20261116190000
//
// Side-by-side runtime base generations on one node. A routed key keeps its
// cache file under its parent name (cacheFileFor), so each generation of a
// runtime base has its own file and marker. The serving vmmd keeps booting
// its own generation while a release pre-stages the next one beside it, and
// a compute rollout that changes a base restarts vmmd without a drain.
//
// Before this change a node kept one file per base under its logical name.
// AdoptCachedGeneration carries that file over to the generation's name by a
// hard link, so the first release with the change downloads nothing it
// already holds. UnpinCachedGenerations drops the eviction pin of the
// generations a release no longer boots, so ordinary eviction reclaims them.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// AdoptCachedGeneration installs the file this cache kept for key under its
// logical name as the entry of key's routed generation, when that file is
// committed as generation and the routed entry is not. It links the file
// rather than copying it: the two names share the bytes until either is
// replaced or evicted. It reports whether it adopted.
func (c *LocalCacheBackend) AdoptCachedGeneration(key, generation string) (bool, error) {
	if err := validateKey(key); err != nil {
		return false, err
	}
	parentKey, routed := c.GenerationRoute(key)
	if !routed {
		return false, nil
	}
	if current, ok, err := c.CachedGeneration(key); err != nil || (ok && current == generation) {
		return false, err
	}
	legacy := CacheFileForKey(c.root, key)
	marker, err := os.ReadFile(legacy + ".generation") //nolint:forbidigo // path is derived from the validated cache key under the backend root.
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("storage: cache: read generation %q: %w", key, err)
	}
	if strings.TrimSpace(string(marker)) != generation || !regularFileExists(legacy) {
		return false, nil
	}
	path, metaPath := c.cacheFileFor(key)
	c.mu.Lock()
	err = c.linkEntryLocked(legacy, path, metaPath, parentKey)
	c.mu.Unlock()
	if err != nil {
		return false, fmt.Errorf("storage: cache: adopt %q as %q: %w", key, parentKey, err)
	}
	if err := c.MarkGeneration(key, generation); err != nil {
		return false, err
	}
	return true, nil
}

// linkEntryLocked installs src as the cache entry at path by a hard link,
// atomically replacing any entry there. Caller holds c.mu.
func (c *LocalCacheBackend) linkEntryLocked(src, path, metaPath, key string) error {
	dir := filepath.Dir(path)
	if err := ensureSharedCacheDir(dir); err != nil {
		return err
	}
	tmp := filepath.Join(dir, fmt.Sprintf(".faas-cache-link-%d-%d", os.Getpid(), time.Now().UnixNano()))
	if err := os.Link(src, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	_ = os.Remove(path + ".generation")
	return os.WriteFile(metaPath, []byte(key), 0o644) //nolint:gosec // cache sidecars are shared with the faas group, like installLocked's.
}

// UnpinCachedGenerations removes the generation marker of every cached entry
// whose key unpin selects, and reports how many it removed. The entry stays
// cached, and evictable: a running guest keeps its own hard link to the
// file, and a release configured for that generation again fetches it.
func (c *LocalCacheBackend) UnpinCachedGenerations(unpin func(key string) bool) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entries, err := c.snapshotCacheLocked()
	if err != nil {
		return 0, fmt.Errorf("storage: cache: unpin generations: %w", err)
	}
	removed := 0
	for _, e := range entries {
		if !e.pinned || !unpin(e.key) {
			continue
		}
		if err := os.Remove(e.path + ".generation"); err != nil && !errors.Is(err, os.ErrNotExist) {
			return removed, fmt.Errorf("storage: cache: unpin %q: %w", e.key, err)
		}
		removed++
	}
	return removed, nil
}
