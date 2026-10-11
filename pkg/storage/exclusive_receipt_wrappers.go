// adr: 568 — receipts stay on the original canonical route, bypassing caches.
package storage

import (
	"context"
	"io"
)

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
