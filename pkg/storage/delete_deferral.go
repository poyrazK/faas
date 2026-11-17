package storage

import (
	"context"
	"fmt"
)

// DeleteDeferrer records that an object of the shared store should be
// deleted, in place of deleting it (ADR-20261115180000): a compute node's
// store credentials then need no delete right, and the control plane's
// object GC deletes the object once it re-checks that nothing references it.
type DeleteDeferrer interface {
	DeferDelete(ctx context.Context, key string) error
}

// GenerationRetireDeferrer records an exclusive artifact's retirement:
// delete exactly this generation, never a replacement written under the
// same key (ADR-20261115180000 slice 2). A deferrer that lacks it makes a
// deferring backend refuse exclusive retirement rather than delete.
type GenerationRetireDeferrer interface {
	DeferRetireGeneration(ctx context.Context, key string, generation int64) error
}

// DeleteGeneration deletes one generation of key from the shared store under
// b, walking composites as DeferDeletes does. A missing generation is done;
// a key that now holds another generation returns ErrArtifactReceiptMismatch
// and is left alone. b must not defer (the control plane's backend).
func DeleteGeneration(ctx context.Context, b StorageBackend, key string, generation int64) error {
	if generation <= 0 {
		return fmt.Errorf("storage: DeleteGeneration needs a positive generation")
	}
	switch be := b.(type) {
	case *GCSStorageBackend:
		if be.deferDelete != nil {
			return fmt.Errorf("storage: DeleteGeneration on a deferring backend")
		}
		return be.deleteGeneration(ctx, key, generation)
	case *LocalCacheBackend:
		return DeleteGeneration(ctx, be.parent, key, generation)
	case *PrefixRouter:
		routed, remainder, _, err := be.dispatch(key)
		if err != nil {
			return err
		}
		return DeleteGeneration(ctx, routed, remainder, generation)
	case *FallbackStorageBackend:
		return DeleteGeneration(ctx, be.primary, key, generation)
	case GenerationDeleter:
		return be.DeleteGeneration(ctx, key, generation)
	default:
		return fmt.Errorf("%w: %T", ErrExclusiveRetireUnsupported, b)
	}
}

// GenerationDeleter is a backend outside this package that deletes one
// object generation, with DeleteGeneration's contract.
type GenerationDeleter interface {
	DeleteGeneration(ctx context.Context, key string, generation int64) error
}

// DeferDeletes makes every shared remote backend under b (GCS, S3, OCI) record
// deletes through d instead of performing them. Composites are walked: the
// read-through cache still evicts its local copy and node-local prefix
// routes still delete their files, since only the shared store loses its
// delete right. The remote backends receive the full logical key (local
// routes strip prefixes; the remote fallback does not), so the recorded key
// is the one the control plane deletes. It returns how many remote backends
// now defer; 0 means b has no shared store (a local single-box layout).
// Call it at wiring time, before b serves requests.
func DeferDeletes(b StorageBackend, d DeleteDeferrer) (int, error) {
	if d == nil {
		return 0, fmt.Errorf("storage: DeferDeletes needs a deferrer")
	}
	switch be := b.(type) {
	case *GCSStorageBackend:
		be.deferDelete = d
		return 1, nil
	case *S3StorageBackend:
		be.deferDelete = d
		return 1, nil
	case *OCIRegistryStorageBackend:
		be.deferDelete = d
		return 1, nil
	case *LocalCacheBackend:
		return DeferDeletes(be.parent, d)
	case *PrefixRouter:
		n := 0
		for prefix, route := range be.routes {
			c, err := DeferDeletes(route, d)
			if err != nil {
				return n, err
			}
			if c > 0 {
				// A route strips its prefix, so the key it would record is
				// not the one the control plane deletes.
				return n, fmt.Errorf("storage: DeferDeletes: prefix route %q leads to a shared store", prefix)
			}
		}
		if be.fallback != nil {
			c, err := DeferDeletes(be.fallback, d)
			if err != nil {
				return n, err
			}
			n += c
		}
		return n, nil
	case *FallbackStorageBackend:
		a, err := DeferDeletes(be.primary, d)
		if err != nil {
			return a, err
		}
		c, err := DeferDeletes(be.fallback, d)
		return a + c, err
	case *LocalStorageBackend:
		return 0, nil
	default:
		// An unknown backend might be shared; refusing beats silently
		// keeping a delete right the operator meant to drop.
		return 0, fmt.Errorf("storage: DeferDeletes does not know backend %T", b)
	}
}
