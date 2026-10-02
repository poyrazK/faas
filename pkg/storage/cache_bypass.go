package storage

import (
	"context"
	"errors"
	"io"
)

// uncachedBackend follows the selected route and strips every local cache while
// retaining routing validation. Mutable objects cannot use immutable cache hits.
func uncachedBackend(backend StorageBackend, key string) (StorageBackend, string, error) {
	if backend == nil {
		return nil, "", errors.New("storage: uncached operation requires a backend")
	}
	for {
		cache, cacheKey, err := CacheBackendForKey(backend, key)
		if err != nil {
			return nil, "", err
		}
		if cache == nil {
			return backend, key, nil
		}
		backend, key = cache.parent, cacheKey
	}
}

func GetUncached(ctx context.Context, backend StorageBackend, key string) (io.ReadCloser, error) {
	selected, key, err := uncachedBackend(backend, key)
	if err != nil {
		return nil, err
	}
	return selected.Get(ctx, key)
}

func PutUncached(ctx context.Context, backend StorageBackend, key string, r io.Reader) error {
	selected, key, err := uncachedBackend(backend, key)
	if err != nil {
		return err
	}
	return selected.Put(ctx, key, r)
}
