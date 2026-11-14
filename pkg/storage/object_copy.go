package storage

import (
	"context"
	"errors"
	"fmt"
)

// ObjectCopier copies one stored object to a new key inside the backend,
// without streaming its bytes through the caller. A deployment retry
// republishes the original source archive under the new build's key; a
// download and re-upload of up to 100 MB inside the API request outlasted the
// CLI's request timeout (ChronoLake report: `release retry` answered a 503
// transport error). Backends without a native copy return
// errors.ErrUnsupported and callers stream instead.
type ObjectCopier interface {
	CopyObject(ctx context.Context, srcKey, dstKey string) error
}

type gcsObjectCopier interface {
	Copy(ctx context.Context, bucket, srcKey, dstKey string) error
}

// CopyObject copies srcKey to dstKey inside the bucket with a server-side
// rewrite. Object metadata (including any artifact encoding) is copied too,
// so Get reads the copy exactly as the original.
func (g *GCSStorageBackend) CopyObject(ctx context.Context, srcKey, dstKey string) error {
	if err := validateKey(srcKey); err != nil {
		return err
	}
	if err := validateKey(dstKey); err != nil {
		return err
	}
	copier, ok := g.store.(gcsObjectCopier)
	if !ok {
		return errors.ErrUnsupported
	}
	if err := copier.Copy(ctx, g.bucket, srcKey, dstKey); err != nil {
		return fmt.Errorf("storage: gcs copy %q to %q: %w", srcKey, dstKey, normalizeGCSError(err))
	}
	return nil
}

func (s *googleGCSArtifactStore) Copy(ctx context.Context, bucket, srcKey, dstKey string) error {
	handle := s.client.Bucket(bucket)
	_, err := handle.Object(dstKey).CopierFrom(handle.Object(srcKey)).Run(ctx)
	return err
}

// CopyObject copies in the remote backend. The destination is not cached
// here; the first read fills it like any other remote object.
func (c *LocalCacheBackend) CopyObject(ctx context.Context, srcKey, dstKey string) error {
	copier, ok := c.parent.(ObjectCopier)
	if !ok {
		return errors.ErrUnsupported
	}
	return copier.CopyObject(ctx, srcKey, dstKey)
}

var (
	_ ObjectCopier = (*GCSStorageBackend)(nil)
	_ ObjectCopier = (*LocalCacheBackend)(nil)
)
