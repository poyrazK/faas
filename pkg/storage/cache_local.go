package storage

import "os"

// CachedLocally reports whether key's bytes are already on this node. Unlike
// LocalPath it never touches the entry's LRU position, so a periodic presence
// check cannot keep an artifact resident that nothing is using, and it never
// contacts a remote parent. known is false when the backend has no local
// notion at all (every read is remote).
func CachedLocally(root StorageBackend, key string) (cached, known bool, err error) {
	cache, rel, err := CacheBackendForKey(root, key)
	if err != nil {
		return false, false, err
	}
	if cache != nil {
		return cache.cachedWithoutTouch(rel), true, nil
	}
	if resolver, ok := root.(LocalPathResolver); ok {
		_, local, err := resolver.LocalPath(key)
		return local, true, err
	}
	return false, false, nil
}

// cachedWithoutTouch is LocalPathWithSource without the LRU touch.
func (c *LocalCacheBackend) cachedWithoutTouch(key string) bool {
	if resolver, ok := c.parent.(LocalPathResolver); ok {
		if _, local, err := resolver.LocalPath(key); err == nil && local {
			return true
		}
	}
	cacheFile, _ := c.cacheFileFor(key)
	fi, err := os.Stat(cacheFile)
	return err == nil && !fi.IsDir() && fi.Size() > 0
}
