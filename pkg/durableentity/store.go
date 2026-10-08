package durableentity

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/objectstorage"
)

// ProviderStore uses an existing provider's private conditional-state
// capability. The bucket must be dedicated to platform-owned entity data.
type ProviderStore struct {
	provider objectstorage.ConditionalStateProvider
	bucket   string
}

func NewProviderStore(provider objectstorage.ConditionalStateProvider, bucket string) (*ProviderStore, error) {
	if provider == nil || bucket == "" {
		return nil, ErrInvalid
	}
	return &ProviderStore{provider: provider, bucket: bucket}, nil
}

func (s *ProviderStore) Get(ctx context.Context, key string, maxBytes int64) ([]byte, string, error) {
	body, version, err := s.provider.ReadStateObject(ctx, s.bucket, key, maxBytes)
	if errors.Is(err, objectstorage.ErrNotFound) {
		err = ErrNotFound
	}
	return body, version, err
}

func (s *ProviderStore) Put(ctx context.Context, key string, body []byte, expectedVersion string) (string, error) {
	version, err := s.provider.WriteStateObject(ctx, s.bucket, key, body, expectedVersion)
	if errors.Is(err, objectstorage.ErrPreconditionFailed) || errors.Is(err, objectstorage.ErrConditionalConflict) {
		err = ErrConflict
	}
	return version, err
}

func (s *ProviderStore) ListEntityPrefixes(ctx context.Context, prefix, cursor string, limit int32) (EntityPrefixPage, error) {
	lister, ok := s.provider.(objectstorage.DelimitedObjectLister)
	if !ok {
		return EntityPrefixPage{}, ErrUnsupported
	}
	page, err := lister.ListObjectsDelimited(ctx, s.bucket, prefix, "/", cursor, limit)
	if err != nil {
		return EntityPrefixPage{}, err
	}
	if len(page.Items) != 0 {
		return EntityPrefixPage{}, ErrCorrupt
	}
	return EntityPrefixPage{Prefixes: page.CommonPrefixes, NextCursor: page.NextCursor}, nil
}

type cleanupProvider interface {
	ListObjects(context.Context, string, string, string, int32) (objectstorage.ObjectPage, error)
	DeleteObject(context.Context, string, string) error
}

type flatListProvider interface {
	ListObjects(context.Context, string, string, string, int32) (objectstorage.ObjectPage, error)
}

func (s *ProviderStore) ListEntityObjects(ctx context.Context, prefix, cursor string, limit int32) (CleanupObjects, error) {
	provider, ok := s.provider.(flatListProvider)
	if !ok {
		return CleanupObjects{}, ErrUnsupported
	}
	page, err := provider.ListObjects(ctx, s.bucket, prefix, cursor, limit)
	if err != nil {
		return CleanupObjects{}, err
	}
	if len(page.CommonPrefixes) != 0 {
		return CleanupObjects{}, ErrCorrupt
	}
	result := CleanupObjects{NextCursor: page.NextCursor, Sizes: make(map[string]int64, len(page.Items))}
	for _, item := range page.Items {
		result.Keys = append(result.Keys, item.Key)
		result.Sizes[item.Key] = item.Size
	}
	return result, nil
}

func (s *ProviderStore) DeleteEntityObject(ctx context.Context, key string) error {
	provider, ok := s.provider.(cleanupProvider)
	if !ok {
		return ErrUnsupported
	}
	return provider.DeleteObject(ctx, s.bucket, key)
}

func writeFailure(operation string, err error) error {
	if errors.Is(err, ErrConflict) {
		return fmt.Errorf("%s: %w", operation, ErrConflict)
	}
	return fmt.Errorf("%s: %w", operation, errors.Join(ErrUncertain, err))
}
