package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
)

// FallbackStorageBackend writes only to primary and reads primary first,
// falling back only when the key is absent. It is the bounded migration seam
// from GHCR to GCS: new artifacts become canonical in GCS immediately while
// deployments that predate the cutover remain restorable from OCI.
//
// Errors other than ErrNotFound never fail open to fallback. A GCS outage must
// remain visible instead of silently reviving an older GHCR generation.
type FallbackStorageBackend struct {
	primary  StorageBackend
	fallback StorageBackend
}

var (
	_ StorageBackend            = (*FallbackStorageBackend)(nil)
	_ LocalArtifactLister       = (*FallbackStorageBackend)(nil)
	_ SnapshotRepositoryIndexer = (*FallbackStorageBackend)(nil)
)

func NewFallbackStorageBackend(primary, fallback StorageBackend) (*FallbackStorageBackend, error) {
	if primary == nil || fallback == nil {
		return nil, errors.New("storage: fallback backend requires primary and fallback")
	}
	if _, ok := primary.(LocalArtifactLister); !ok {
		return nil, errors.New("storage: fallback primary does not implement List")
	}
	if _, ok := fallback.(LocalArtifactLister); !ok {
		return nil, errors.New("storage: fallback secondary does not implement List")
	}
	return &FallbackStorageBackend{primary: primary, fallback: fallback}, nil
}

func (b *FallbackStorageBackend) Put(ctx context.Context, key string, r io.Reader) error {
	return b.primary.Put(ctx, key, r)
}

func (b *FallbackStorageBackend) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	reader, err := b.primary.Get(ctx, key)
	if err == nil || !IsNotFound(err) {
		return reader, err
	}
	reader, fallbackErr := b.fallback.Get(ctx, key)
	if fallbackErr != nil {
		return nil, fmt.Errorf("storage: fallback get %q after primary miss: %w", key, fallbackErr)
	}
	return reader, nil
}

func (b *FallbackStorageBackend) Delete(ctx context.Context, key string) error {
	primaryErr := b.primary.Delete(ctx, key)
	fallbackErr := b.fallback.Delete(ctx, key)
	if primaryErr == nil && fallbackErr == nil {
		return nil
	}
	return fmt.Errorf("storage: fallback delete %q: %w", key, errors.Join(primaryErr, fallbackErr))
}

func (b *FallbackStorageBackend) List(ctx context.Context, prefix string) ([]string, error) {
	primaryKeys, primaryErr := b.primary.(LocalArtifactLister).List(ctx, prefix)
	fallbackKeys, fallbackErr := b.fallback.(LocalArtifactLister).List(ctx, prefix)
	if primaryErr != nil || fallbackErr != nil {
		return nil, fmt.Errorf("storage: fallback list %q: %w", prefix, errors.Join(primaryErr, fallbackErr))
	}
	seen := make(map[string]struct{}, len(primaryKeys)+len(fallbackKeys))
	for _, key := range primaryKeys {
		seen[key] = struct{}{}
	}
	for _, key := range fallbackKeys {
		seen[key] = struct{}{}
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys, nil
}

func (b *FallbackStorageBackend) ReconcileSnapshotRepositoryIndex(ctx context.Context, deploymentIDs []string) error {
	var errs []error
	if indexer, ok := b.primary.(SnapshotRepositoryIndexer); ok {
		errs = append(errs, indexer.ReconcileSnapshotRepositoryIndex(ctx, deploymentIDs))
	}
	if indexer, ok := b.fallback.(SnapshotRepositoryIndexer); ok {
		errs = append(errs, indexer.ReconcileSnapshotRepositoryIndex(ctx, deploymentIDs))
	}
	return errors.Join(errs...)
}
