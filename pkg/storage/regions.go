package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/region"
)

var ErrRegionNotConfigured = errors.New("artifact region not configured")

// RegionRouter reserves regions/<id>/ for explicit stores. Its guard sits
// outside the shared cache so removing a route cannot expose old cached data.
// Cache entries use the full key; only the storage driver sees a stripped key.
type RegionRouter struct {
	routes  map[string]StorageBackend
	legacy  StorageBackend
	backend StorageBackend
}

func NewRegionRouter(routes map[string]StorageBackend, legacy StorageBackend) (*RegionRouter, error) {
	if legacy == nil {
		return nil, fmt.Errorf("storage: regional router requires legacy backend")
	}
	copyRoutes := make(map[string]StorageBackend, len(routes))
	prefixes := make(map[string]StorageBackend, len(routes))
	for id, backend := range routes {
		if id == "" || !region.Valid(id) || backend == nil {
			return nil, fmt.Errorf("storage: invalid regional route %q", id)
		}
		copyRoutes[id] = backend
		prefixes[region.ArtifactKey(id, "")] = backend
	}
	router, err := NewPrefixRouter(prefixes, legacy)
	if err != nil {
		return nil, err
	}
	return &RegionRouter{routes: copyRoutes, legacy: legacy, backend: router}, nil
}

func (r *RegionRouter) validate(key string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	id, _, err := region.SplitArtifactKey(key)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidKey, err)
	}
	if id != "" && r.routes[id] == nil {
		return fmt.Errorf("storage: region %q: %w", id, ErrRegionNotConfigured)
	}
	return nil
}

func (r *RegionRouter) Put(ctx context.Context, key string, body io.Reader) error {
	if err := r.validate(key); err != nil {
		return err
	}
	return r.backend.Put(ctx, key, body)
}

func (r *RegionRouter) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := r.validate(key); err != nil {
		return nil, err
	}
	return r.backend.Get(ctx, key)
}

func (r *RegionRouter) Delete(ctx context.Context, key string) error {
	if err := r.validate(key); err != nil {
		return err
	}
	return r.backend.Delete(ctx, key)
}

func (r *RegionRouter) Exists(ctx context.Context, key string) (bool, error) {
	if err := r.validate(key); err != nil {
		return false, err
	}
	checker, ok := r.backend.(ExistenceChecker)
	if !ok {
		return false, ErrExistenceUnsupported
	}
	return checker.Exists(ctx, key)
}

func (r *RegionRouter) LocalPath(key string) (string, bool, error) {
	p, _, ok, err := r.LocalPathWithSource(key)
	return p, ok, err
}

func (r *RegionRouter) LocalPathWithSource(key string) (string, LocalPathSource, bool, error) {
	if err := r.validate(key); err != nil {
		return "", "", false, err
	}
	if resolver, ok := r.backend.(LocalPathSourceResolver); ok {
		return resolver.LocalPathWithSource(key)
	}
	if resolver, ok := r.backend.(LocalPathResolver); ok {
		p, ok, err := resolver.LocalPath(key)
		return p, "backend_local", ok, err
	}
	return "", "", false, nil
}

// List enumerates the selected canonical namespace, using the authoritative
// store rather than the subset currently resident in the local cache.
func (r *RegionRouter) List(ctx context.Context, prefix string) ([]string, error) {
	if prefix == "" {
		return r.ListArtifactKeys(ctx, "")
	}
	if err := validateKey(strings.TrimSuffix(prefix, "/")); err != nil {
		return nil, err
	}
	if prefix == "regions/" {
		return r.listRegions(ctx, "")
	}
	id, relative, err := region.SplitArtifactKey(prefix)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidKey, err)
	}
	backend, namespace := r.legacy, ""
	if id != "" {
		backend, namespace = r.routes[id], region.ArtifactKey(id, "")
		if backend == nil {
			return nil, fmt.Errorf("storage: region %q: %w", id, ErrRegionNotConfigured)
		}
	}
	return listRegionBackend(ctx, backend, namespace, relative)
}

func listRegionBackend(ctx context.Context, backend StorageBackend, namespace, prefix string) ([]string, error) {
	lister, ok := backend.(LocalArtifactLister)
	if !ok {
		return nil, ErrInvalidKey
	}
	keys, err := lister.List(ctx, prefix)
	if err != nil {
		return nil, err
	}
	var result []string
	for _, key := range keys {
		// Never widen destructive cleanup based on a driver's broad listing.
		if err := validateKey(key); err != nil {
			return nil, err
		}
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		if key == "regions" || strings.HasPrefix(key, "regions/") {
			continue
		}
		result = append(result, namespace+key)
	}
	sort.Strings(result)
	return result, nil
}

func (r *RegionRouter) listRegions(ctx context.Context, prefix string) ([]string, error) {
	var result []string
	ids := make([]string, 0, len(r.routes))
	for id := range r.routes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		keys, err := listRegionBackend(ctx, r.routes[id], region.ArtifactKey(id, ""), prefix)
		if err != nil {
			return nil, fmt.Errorf("storage: list region %q: %w", id, err)
		}
		result = append(result, keys...)
	}
	return result, nil
}

// ListArtifactKeys enumerates a logical artifact class across regional stores,
// retaining the full routing key on every result. List("jobs/") itself keeps
// ordinary prefix semantics; lifecycle sweeps use this explicit fan-out.
func (r *RegionRouter) ListArtifactKeys(ctx context.Context, prefix string) ([]string, error) {
	if prefix != "" {
		if err := validateKey(strings.TrimSuffix(prefix, "/")); err != nil {
			return nil, err
		}
		if id, _, err := region.SplitArtifactKey(prefix); err != nil || id != "" {
			return nil, ErrInvalidKey
		}
	}
	legacy, err := listRegionBackend(ctx, r.legacy, "", prefix)
	if err != nil {
		return nil, err
	}
	regional, err := r.listRegions(ctx, prefix)
	if err != nil {
		return nil, err
	}
	keys := append(legacy, regional...)
	sort.Strings(keys)
	return keys, nil
}

func ListArtifactKeys(ctx context.Context, backend StorageBackend, prefix string) ([]string, error) {
	if lister, ok := backend.(interface {
		ListArtifactKeys(context.Context, string) ([]string, error)
	}); ok {
		return lister.ListArtifactKeys(ctx, prefix)
	}
	lister, ok := backend.(LocalArtifactLister)
	if !ok {
		return nil, ErrInvalidKey
	}
	return lister.List(ctx, prefix)
}

func (r *RegionRouter) ReconcileSnapshotRepositoryIndex(ctx context.Context, deploymentIDs []string) error {
	if indexer, ok := r.legacy.(SnapshotRepositoryIndexer); ok {
		if err := indexer.ReconcileSnapshotRepositoryIndex(ctx, deploymentIDs); err != nil {
			return err
		}
	}
	// Regional keys are new and every OCI Put registers its repository. Only
	// the legacy store needs historical database IDs; initialise empty regional
	// indexes without inventing repositories for another region's deployments.
	for id, backend := range r.routes {
		if indexer, ok := backend.(SnapshotRepositoryIndexer); ok {
			if err := indexer.ReconcileSnapshotRepositoryIndex(ctx, nil); err != nil {
				return fmt.Errorf("storage: reconcile region %q: %w", id, err)
			}
		}
	}
	return nil
}
