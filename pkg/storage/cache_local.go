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
		_, cached := cache.localPathWithoutTouch(rel)
		return cached, true, nil
	}
	if resolver, ok := root.(LocalPathResolver); ok {
		_, local, err := resolver.LocalPath(key)
		return local, true, err
	}
	return false, false, nil
}

// LocalPathWithoutTouch is LocalPath for a caller that only inspects which
// file a key resolves to, such as vmmd's periodic ADR-510 digest primer: it
// never advances the entry's LRU position and never contacts a remote parent.
func LocalPathWithoutTouch(root StorageBackend, key string) (string, bool, error) {
	cache, rel, err := CacheBackendForKey(root, key)
	if err != nil {
		return "", false, err
	}
	if cache != nil {
		path, local := cache.localPathWithoutTouch(rel)
		return path, local, nil
	}
	if resolver, ok := root.(LocalPathResolver); ok {
		return resolver.LocalPath(key)
	}
	return "", false, nil
}

// localPathWithoutTouch is LocalPathWithSource without the LRU touch.
func (c *LocalCacheBackend) localPathWithoutTouch(key string) (string, bool) {
	if resolver, ok := c.parent.(LocalPathResolver); ok {
		if path, local, err := resolver.LocalPath(key); err == nil && local {
			return path, true
		}
	}
	cacheFile, _ := c.cacheFileFor(key)
	fi, err := os.Stat(cacheFile)
	if err != nil || fi.IsDir() || fi.Size() == 0 {
		return "", false
	}
	return cacheFile, true
}
