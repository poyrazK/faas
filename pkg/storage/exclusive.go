// adr: 568 — native capture publication requires explicit immutable writers.
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
)

// ExclusivePutBackend publishes a fresh immutable object without named
// temporary files. It consumes the original reader synchronously, never
// reopens its Name, and rejects an existing destination. An error after an
// uncertain commit never authorizes overwriting or deleting that destination.
// Callers own durable key intent, source locks and cleanup authority; this
// capability supplies no native capture, restore or qualification evidence.
type ExclusivePutBackend interface {
	CheckExclusivePut(context.Context, string) error
	PutExclusive(context.Context, string, io.Reader, int64) error
}

var (
	ErrExclusivePutUnsupported = errors.New("storage: exclusive publication unsupported")
	ErrArtifactExists          = errors.New("storage: immutable artifact already exists")
)

// CheckExclusivePut refuses unsupported delegates before any producer effect.
// It is a capability check, not a reservation or an existence receipt.
func CheckExclusivePut(ctx context.Context, backend StorageBackend, key string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	publisher, ok := backend.(ExclusivePutBackend)
	if !ok {
		return ErrExclusivePutUnsupported
	}
	return publisher.CheckExclusivePut(ctx, key)
}

func PutExclusive(ctx context.Context, backend StorageBackend, key string, reader io.Reader, size int64) error {
	if err := CheckExclusivePut(ctx, backend, key); err != nil {
		return err
	}
	if reader == nil || size <= 0 {
		return errors.New("storage: exclusive publication requires a nonempty original reader")
	}
	return backend.(ExclusivePutBackend).PutExclusive(ctx, key, reader, size)
}

// A source error at its last bytes must prevent publication, and a larger or
// shorter source must never acquire metadata describing a different size.
type exactArtifactReader struct {
	source    io.Reader
	remaining int64
	complete  bool
}

func (r *exactArtifactReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if r.remaining == 0 {
		var extra [1]byte
		n, err := r.source.Read(extra[:])
		if n > 0 {
			return 0, errors.New("storage: exclusive source exceeds its original size")
		}
		r.complete = err == io.EOF //nolint:errorlint // Only the original EOF completes publication.
		return 0, err
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.source.Read(p)
	r.remaining -= int64(n)
	if err == io.EOF && r.remaining > 0 { //nolint:errorlint // Wrapped source errors cannot complete publication.
		err = fmt.Errorf("storage: exclusive source is shorter than its original size: %w", io.ErrUnexpectedEOF)
	}
	r.complete = err == io.EOF && r.remaining == 0 //nolint:errorlint // Only the original EOF completes publication.
	return n, err
}

func (r *PrefixRouter) CheckExclusivePut(ctx context.Context, key string) error {
	b, remainder, _, err := r.dispatch(key)
	if err != nil {
		return err
	}
	return CheckExclusivePut(ctx, b, remainder)
}

func (r *PrefixRouter) PutExclusive(ctx context.Context, key string, reader io.Reader, size int64) error {
	b, remainder, _, err := r.dispatch(key)
	if err != nil {
		return err
	}
	return PutExclusive(ctx, b, remainder, reader, size)
}

func (b *FallbackStorageBackend) CheckExclusivePut(ctx context.Context, key string) error {
	return CheckExclusivePut(ctx, b.primary, key)
}

func (b *FallbackStorageBackend) PutExclusive(ctx context.Context, key string, reader io.Reader, size int64) error {
	return PutExclusive(ctx, b.primary, key, reader, size)
}

func (c *LocalCacheBackend) CheckExclusivePut(ctx context.Context, key string) error {
	return CheckExclusivePut(ctx, c.parent, key)
}

func (c *LocalCacheBackend) PutExclusive(ctx context.Context, key string, reader io.Reader, size int64) error {
	// Cache population creates named spools and generation files. The explicit
	// native capability publishes only to the canonical parent; Get may fill
	// the ordinary cache later under its existing ownership contract.
	return PutExclusive(ctx, c.parent, key, reader, size)
}
