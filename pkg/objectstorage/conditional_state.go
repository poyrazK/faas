package objectstorage

import "context"

// ConditionalStateProvider is an internal capability for private platform
// state. Reads return the body and version from the SAME operation. Writes
// conditionally create when expectedETag is empty, otherwise compare-and-swap.
// Versions are opaque: S3 uses ETags; GCS uses native content generations.
// A successful write is durable according to the qualified bucket's contract.
// Implementations must not retry writes after dispatch. Errors other than a
// definite condition rejection leave the write's outcome uncertain.
type ConditionalStateProvider interface {
	ReadStateObject(ctx context.Context, bucket, key string, maxBytes int64) (body []byte, etag string, err error)
	WriteStateObject(ctx context.Context, bucket, key string, body []byte, expectedETag string) (etag string, err error)
}
