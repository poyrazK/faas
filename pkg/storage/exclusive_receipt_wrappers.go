// adr: 568 — receipts stay on the original canonical route, bypassing caches.
package storage

import (
	"context"
	"io"
)

func (r *PrefixRouter) cohortRoute(objects [4]ExclusiveArtifactReceipt) (StorageBackend, [4]ExclusiveArtifactReceipt, error) {
	var routed [4]ExclusiveArtifactReceipt
	var backend StorageBackend
	var prefix string
	for i, object := range objects {
		b, remainder, p, err := r.dispatch(object.Key)
		if err != nil {
			return nil, routed, err
		}
		if i > 0 && p != prefix {
			return nil, routed, ErrArtifactReceiptMismatch
		}
		backend, prefix = b, p
		routed[i] = object
		routed[i].Key = remainder
	}
	return backend, routed, nil
}

func (r *PrefixRouter) CheckExclusiveArtifactCohortRetirement(ctx context.Context, captureID string, objects [4]ExclusiveArtifactReceipt) error {
	b, routed, err := r.cohortRoute(objects)
	if err != nil {
		return err
	}
	return CheckExclusiveArtifactCohortRetirement(ctx, b, captureID, routed)
}

func (r *PrefixRouter) RetireExclusiveArtifactCohort(ctx context.Context, captureID string, objects [4]ExclusiveArtifactReceipt) error {
	b, routed, err := r.cohortRoute(objects)
	if err != nil {
		return err
	}
	return RetireExclusiveArtifactCohort(ctx, b, captureID, routed)
}

func (r *PrefixRouter) CheckExclusiveArtifact(ctx context.Context, key string) error {
	b, remainder, _, err := r.dispatch(key)
	if err != nil {
		return err
	}
	return CheckExclusiveArtifact(ctx, b, remainder)
}

func (r *PrefixRouter) PutExclusiveArtifact(ctx context.Context, key string, source io.Reader, size int64) (ExclusiveArtifactReceipt, error) {
	b, remainder, _, err := r.dispatch(key)
	if err != nil {
		return ExclusiveArtifactReceipt{}, err
	}
	receipt, err := PutExclusiveArtifact(ctx, b, remainder, source, size)
	if err != nil {
		return ExclusiveArtifactReceipt{}, err
	}
	receipt.Key = key
	return receipt, nil
}

func (r *PrefixRouter) GetExclusiveArtifact(ctx context.Context, receipt ExclusiveArtifactReceipt) (io.ReadCloser, error) {
	b, remainder, _, err := r.dispatch(receipt.Key)
	if err != nil {
		return nil, err
	}
	receipt.Key = remainder
	return GetExclusiveArtifact(ctx, b, receipt)
}

func (r *PrefixRouter) RetireExclusiveArtifact(ctx context.Context, receipt ExclusiveArtifactReceipt) error {
	b, remainder, _, err := r.dispatch(receipt.Key)
	if err != nil {
		return err
	}
	receipt.Key = remainder
	return RetireExclusiveArtifact(ctx, b, receipt)
}

func (r *PrefixRouter) CheckExclusiveArtifactRetirement(ctx context.Context, receipt ExclusiveArtifactReceipt) error {
	b, remainder, _, err := r.dispatch(receipt.Key)
	if err != nil {
		return err
	}
	receipt.Key = remainder
	return CheckExclusiveArtifactRetirement(ctx, b, receipt)
}

func (b *FallbackStorageBackend) CheckExclusiveArtifact(ctx context.Context, key string) error {
	return CheckExclusiveArtifact(ctx, b.primary, key)
}
func (b *FallbackStorageBackend) PutExclusiveArtifact(ctx context.Context, key string, source io.Reader, size int64) (ExclusiveArtifactReceipt, error) {
	return PutExclusiveArtifact(ctx, b.primary, key, source, size)
}
func (b *FallbackStorageBackend) GetExclusiveArtifact(ctx context.Context, receipt ExclusiveArtifactReceipt) (io.ReadCloser, error) {
	return GetExclusiveArtifact(ctx, b.primary, receipt)
}
func (b *FallbackStorageBackend) RetireExclusiveArtifact(ctx context.Context, receipt ExclusiveArtifactReceipt) error {
	return RetireExclusiveArtifact(ctx, b.primary, receipt)
}
func (b *FallbackStorageBackend) CheckExclusiveArtifactRetirement(ctx context.Context, receipt ExclusiveArtifactReceipt) error {
	return CheckExclusiveArtifactRetirement(ctx, b.primary, receipt)
}
func (b *FallbackStorageBackend) CheckExclusiveArtifactCohortRetirement(ctx context.Context, captureID string, objects [4]ExclusiveArtifactReceipt) error {
	return CheckExclusiveArtifactCohortRetirement(ctx, b.primary, captureID, objects)
}
func (b *FallbackStorageBackend) RetireExclusiveArtifactCohort(ctx context.Context, captureID string, objects [4]ExclusiveArtifactReceipt) error {
	return RetireExclusiveArtifactCohort(ctx, b.primary, captureID, objects)
}

func (c *LocalCacheBackend) CheckExclusiveArtifact(ctx context.Context, key string) error {
	return CheckExclusiveArtifact(ctx, c.parent, key)
}
func (c *LocalCacheBackend) PutExclusiveArtifact(ctx context.Context, key string, source io.Reader, size int64) (ExclusiveArtifactReceipt, error) {
	return PutExclusiveArtifact(ctx, c.parent, key, source, size)
}
func (c *LocalCacheBackend) GetExclusiveArtifact(ctx context.Context, receipt ExclusiveArtifactReceipt) (io.ReadCloser, error) {
	return GetExclusiveArtifact(ctx, c.parent, receipt)
}
func (c *LocalCacheBackend) RetireExclusiveArtifact(ctx context.Context, receipt ExclusiveArtifactReceipt) error {
	return RetireExclusiveArtifact(ctx, c.parent, receipt)
}
func (c *LocalCacheBackend) CheckExclusiveArtifactRetirement(ctx context.Context, receipt ExclusiveArtifactReceipt) error {
	return CheckExclusiveArtifactRetirement(ctx, c.parent, receipt)
}
func (c *LocalCacheBackend) CheckExclusiveArtifactCohortRetirement(ctx context.Context, captureID string, objects [4]ExclusiveArtifactReceipt) error {
	return CheckExclusiveArtifactCohortRetirement(ctx, c.parent, captureID, objects)
}
func (c *LocalCacheBackend) RetireExclusiveArtifactCohort(ctx context.Context, captureID string, objects [4]ExclusiveArtifactReceipt) error {
	return RetireExclusiveArtifactCohort(ctx, c.parent, captureID, objects)
}
