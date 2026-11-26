package storage

// Node-first captures (ADR-20261122132055 §2). A persistent sandbox
// session's pause capture is written to this node's cache only, so the pause
// waits for no upload, and is uploaded to the parent in the background. Until
// then the entry is local-only: a ".local" sidecar marks it, eviction never
// takes it (nothing else holds its bytes), and PublishLocal clears the mark
// once the parent holds a copy. An upload given up on clears it too
// (UnpinLocal): the entry stays in the cache as an ordinary, evictable one,
// so a capture nobody could upload does not hold the node's disk for good.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// localOnlySuffix names the sidecar of a local-only cache entry. It holds
// the entry's key.
const localOnlySuffix = ".local"

// PutLocal writes key into this node's cache only, marked local-only. It
// fails rather than falling back to the parent: a node-first write that
// cannot be cached has nowhere to be.
func (c *LocalCacheBackend) PutLocal(ctx context.Context, key string, r io.Reader) error {
	if err := validateKey(key); err != nil {
		return err
	}
	path, metaPath := c.cacheFileFor(key)
	tmp, err := c.spoolFile(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("storage: cache: put local %q: spool: %w", key, err)
	}
	tmpPath := tmp.Name()
	keep := false
	defer func() {
		if !keep {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
		}
	}()
	if _, cloned := cloneSpool(tmp, r, key); !cloned {
		if source, ok := r.(DriveDeltaSource); ok {
			r = source.File
		}
		if _, err := copyArtifactContext(ctx, tmp, r, key); err != nil {
			return fmt.Errorf("storage: cache: put local %q: spool: %w", key, err)
		}
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("storage: cache: put local %q: fsync spool: %w", key, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("storage: cache: put local %q: close spool: %w", key, err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.pending, key)
	// The mark goes first, so the entry is pinned from the moment it is
	// visible.
	if err := os.WriteFile(path+localOnlySuffix, []byte(key), 0o644); err != nil {
		return fmt.Errorf("storage: cache: put local %q: mark: %w", key, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(path + localOnlySuffix)
		return fmt.Errorf("storage: cache: put local %q: install: %w", key, err)
	}
	keep = true
	_ = os.Chmod(path, 0o644)
	_ = os.Remove(path + ".generation")
	if err := os.WriteFile(metaPath, []byte(c.parentKey(key)), 0o644); err != nil {
		return fmt.Errorf("storage: cache: put local %q: sidecar: %w", key, err)
	}
	_ = c.enforceBudgetLocked(key)
	return nil
}

// LocalOnly reports whether key is a local-only entry the parent has not
// been given yet.
func (c *LocalCacheBackend) LocalOnly(key string) bool {
	if validateKey(key) != nil {
		return false
	}
	path, _ := c.cacheFileFor(key)
	_, err := os.Stat(path + localOnlySuffix)
	return err == nil
}

// PublishLocal uploads a local-only entry to the parent and clears its mark.
// It reports false for a key that is not local-only. A key deleted while it
// was uploading is deleted from the parent again, so the upload leaves no
// orphan.
func (c *LocalCacheBackend) PublishLocal(ctx context.Context, key string) (bool, error) {
	if !c.LocalOnly(key) {
		return false, nil
	}
	path, _ := c.cacheFileFor(key)
	f, err := os.Open(path) //nolint:forbidigo // path is derived from the validated cache key
	if err != nil {
		return false, fmt.Errorf("storage: cache: publish local %q: %w", key, err)
	}
	defer func() { _ = f.Close() }()
	parentKey := c.parentKey(key)
	if err := c.parent.Put(ctx, parentKey, f); err != nil {
		return false, fmt.Errorf("storage: cache: publish local %q: parent: %w", key, err)
	}
	c.mu.Lock()
	err = os.Remove(path + localOnlySuffix)
	c.mu.Unlock()
	if errors.Is(err, os.ErrNotExist) {
		// Deleted meanwhile: the parent's copy has no owner.
		if derr := c.parent.Delete(ctx, parentKey); derr != nil && !errors.Is(derr, ErrNotFound) {
			return false, fmt.Errorf("storage: cache: publish local %q: delete orphan: %w", key, derr)
		}
		return false, nil
	}
	return true, nil
}

// UnpinLocal clears key's local-only mark without uploading it: the entry
// is left to the cache's eviction. A key that is not local-only is left
// alone.
func (c *LocalCacheBackend) UnpinLocal(key string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	path, _ := c.cacheFileFor(key)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := os.Remove(path + localOnlySuffix); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("storage: cache: unpin local %q: %w", key, err)
	}
	return nil
}

// localOnlyAge is how long ago key's local-only entry was written.
func (c *LocalCacheBackend) localOnlyAge(key string) (time.Duration, error) {
	path, _ := c.cacheFileFor(key)
	info, err := os.Stat(path + localOnlySuffix)
	if err != nil {
		return 0, err
	}
	return time.Since(info.ModTime()), nil
}

// LocalOnlyKeys lists the local-only entries whose keys start with prefix,
// so a restarted process can finish their uploads.
func (c *LocalCacheBackend) LocalOnlyKeys(prefix string) ([]string, error) {
	buckets, err := os.ReadDir(c.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("storage: cache: list local: %w", err)
	}
	var keys []string
	for _, b := range buckets {
		if !b.IsDir() || len(b.Name()) != 2 {
			continue
		}
		files, err := os.ReadDir(filepath.Join(c.root, b.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), localOnlySuffix) {
				continue
			}
			key, err := os.ReadFile(filepath.Join(c.root, b.Name(), f.Name())) //nolint:forbidigo // a sidecar under the cache root
			if err != nil || !strings.HasPrefix(string(key), prefix) || validateKey(string(key)) != nil {
				continue
			}
			keys = append(keys, string(key))
		}
	}
	sort.Strings(keys)
	return keys, nil
}

// NodeFirstBackend is the store a node-first capture is published to: a
// key the root keeps in a node cache is written there only (PutLocal); any
// other key, and every read and delete, goes to the root as usual.
type NodeFirstBackend struct{ Root StorageBackend }

// NodeFirst wraps root for node-first writes.
func NodeFirst(root StorageBackend) NodeFirstBackend { return NodeFirstBackend{Root: root} }

func (b NodeFirstBackend) Put(ctx context.Context, key string, r io.Reader) error {
	cache, rel, err := CacheBackendForKey(b.Root, key)
	if err != nil {
		return err
	}
	if cache == nil {
		return b.Root.Put(ctx, key, r)
	}
	return cache.PutLocal(ctx, rel, r)
}

func (b NodeFirstBackend) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	return b.Root.Get(ctx, key)
}

func (b NodeFirstBackend) Delete(ctx context.Context, key string) error {
	return b.Root.Delete(ctx, key)
}

// PublishLocal uploads root's local-only entry of key, if it has one.
func PublishLocal(ctx context.Context, root StorageBackend, key string) (bool, error) {
	cache, rel, err := CacheBackendForKey(root, key)
	if err != nil || cache == nil {
		return false, err
	}
	return cache.PublishLocal(ctx, rel)
}

// UnpinLocal clears root's local-only mark of key, if it has one.
func UnpinLocal(root StorageBackend, key string) error {
	cache, rel, err := CacheBackendForKey(root, key)
	if err != nil || cache == nil {
		return err
	}
	return cache.UnpinLocal(rel)
}

// LocalOnly reports whether root keeps key only in this node's cache.
func LocalOnly(root StorageBackend, key string) bool {
	cache, rel, err := CacheBackendForKey(root, key)
	return err == nil && cache != nil && cache.LocalOnly(rel)
}

// PublishLocalPrefix uploads every local-only entry of root under prefix
// (a key prefix ending in a partial path segment, such as
// "snap/sandbox-session-"), for a restarted process. It reports how many it
// uploaded. An entry written more than maxAge ago is past its upload's
// window and is unpinned instead (maxAge 0 uploads every entry).
func PublishLocalPrefix(ctx context.Context, root StorageBackend, prefix string, maxAge time.Duration) (int, error) {
	const probe = "probe"
	cache, rel, err := CacheBackendForKey(root, prefix+probe)
	if err != nil || cache == nil {
		return 0, err
	}
	keys, err := cache.LocalOnlyKeys(strings.TrimSuffix(rel, probe))
	if err != nil {
		return 0, err
	}
	var published int
	var errs []error
	for _, key := range keys {
		if maxAge > 0 {
			if age, err := cache.localOnlyAge(key); err == nil && age > maxAge {
				if err := cache.UnpinLocal(key); err != nil {
					errs = append(errs, err)
				}
				continue
			}
		}
		ok, err := cache.PublishLocal(ctx, key)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if ok {
			published++
		}
	}
	return published, errors.Join(errs...)
}
