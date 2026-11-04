package fcvm

// adr: 20261026181418

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/onebox-faas/faas/pkg/storage"
)

// baseGroupCacheKeys resolves keys to their names in cache, which must own
// every one of them: vmmd adopts a runtime base and its sidecars as one group.
func baseGroupCacheKeys(root storage.StorageBackend, cache *storage.LocalCacheBackend, keys []string) ([]string, error) {
	out := make([]string, len(keys))
	for i, key := range keys {
		owner, rel, err := storage.CacheBackendForKey(root, key)
		if err != nil {
			return nil, fmt.Errorf("resolve cache route for %q: %w", key, err)
		}
		if owner == nil || owner != cache {
			return nil, fmt.Errorf("runtime base group key %q is not owned by the base's read-through cache", key)
		}
		out[i] = rel
	}
	return out, nil
}

// adoptBaseScan installs the shared scan sidecar from as this node's cached
// scanRel and requires it to name expected. A missing sidecar keeps
// storage.ErrNotFound in its error chain.
func (m *Manager) adoptBaseScan(ctx context.Context, cache *storage.LocalCacheBackend, scanKey, scanRel, from, expected string) error {
	rc, err := cache.RefreshFrom(ctx, scanRel, from)
	if err != nil {
		return fmt.Errorf("refresh runtime base artifact %q: %w", from, err)
	}
	if err := rc.Close(); err != nil {
		return fmt.Errorf("close refreshed runtime base artifact %q: %w", from, err)
	}
	scan, err := m.readBaseScan(ctx, scanKey)
	if err != nil {
		return fmt.Errorf("read refreshed scan sidecar: %w", err)
	}
	if !scanNamesConfiguredBase(scan.Image, expected) {
		return fmt.Errorf("refreshed scan generation %q does not match configured runtime base %q", scan.Image, expected)
	}
	return nil
}

// legacyScanNames reports whether the legacy shared scan sidecar of scanRel
// names expected, read without touching this node's cache.
func (m *Manager) legacyScanNames(ctx context.Context, cache *storage.LocalCacheBackend, scanRel, expected string) bool {
	rc, err := cache.GetParent(ctx, scanRel)
	if err != nil {
		return false
	}
	defer func() { _ = rc.Close() }()
	var scan baseScanSidecar
	if json.NewDecoder(io.LimitReader(rc, 1<<20)).Decode(&scan) != nil {
		return false
	}
	return scanNamesConfiguredBase(scan.Image, expected)
}
