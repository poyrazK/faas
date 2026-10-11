package storage

// Snapshot drive block sharing — ADR-633.

import (
	"context"
	"errors"
	"fmt"
	"os"
)

// errDedupeUnsupported stops a dedupe pass the filesystem refuses outright.
var errDedupeUnsupported = errors.New("storage: block dedupe unsupported")

// cloneSpool fills the empty spool tmp from r with a copy-on-write clone when
// r is an unread regular file on the same reflink-capable filesystem. It
// reports the cloned length; ok is false when the caller must copy instead.
func cloneSpool(tmp *os.File, r any, key string) (written int64, ok bool) {
	src, isFile := r.(*os.File)
	if !isFile || !isSparseArtifactKey(key) {
		return 0, false
	}
	info, err := src.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return 0, false
	}
	// FICLONE copies the whole file regardless of the read offset, so it is
	// only equivalent to copying r when nothing has been read yet.
	if pos, err := src.Seek(0, 1); err != nil || pos != 0 {
		return 0, false
	}
	if !cloneInto(tmp, src) {
		return 0, false
	}
	return info.Size(), true
}

// ShareUnchangedBlocks makes the cached copy of key share every block whose
// bytes equal the same block of baseKey's cached copy, and returns the bytes
// it shared. A snapshot drive fetched from the parent arrives as a full copy
// of its app layer plus the guest's writes; this hands the unchanged blocks
// back to the layer. It is a no-op when either key is not cached locally or
// the filesystem cannot share blocks.
func ShareUnchangedBlocks(ctx context.Context, backend StorageBackend, key, baseKey string) (int64, error) {
	path, ok, err := cachedPath(backend, key)
	if err != nil || !ok {
		return 0, err
	}
	basePath, ok, err := cachedPath(backend, baseKey)
	if err != nil || !ok {
		return 0, err
	}
	shared, err := dedupeFile(ctx, path, basePath)
	if errors.Is(err, errDedupeUnsupported) {
		return shared, nil
	}
	if err != nil {
		return shared, fmt.Errorf("storage: share %q blocks with %q: %w", key, baseKey, err)
	}
	return shared, nil
}

func cachedPath(backend StorageBackend, key string) (string, bool, error) {
	cache, rel, err := CacheBackendForKey(backend, key)
	if err != nil || cache == nil {
		return "", false, err
	}
	return cache.LocalPath(rel)
}
