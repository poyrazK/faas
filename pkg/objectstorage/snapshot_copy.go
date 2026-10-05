package objectstorage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"io"
)

// VersionedObjectReader reads the exact immutable source generation in a
// manifest. A live-key read would make verification race with production writes.
type VersionedObjectReader interface {
	ReadObjectVersion(context.Context, string, string, string) (io.ReadCloser, error)
}

type ObjectSnapshotCopier interface {
	CrossBucketObjectCopier
	ObjectReader
	VersionedObjectReader
}

var ErrObjectSnapshotCopyMismatch = errors.New("object storage: copied object differs from pinned source version")

type VerifiedObjectCopy struct {
	CopyObjectResult
	SHA256 string
}

// CopyAndVerifyObjectVersion copies one pinned object and streams both sides
// through SHA-256. It checks source and destination lengths against the
// captured manifest, so an upstream provider's ETag or copy response cannot
// substitute for content verification.
func CopyAndVerifyObjectVersion(ctx context.Context, provider ObjectSnapshotCopier, sourceBucket, destinationBucket string, item ObjectVersion) (VerifiedObjectCopy, error) {
	if provider == nil || sourceBucket == "" || destinationBucket == "" || sourceBucket == destinationBucket ||
		!ValidKey(item.Key) || item.VersionID == "" || item.Size < 0 || item.Deleted {
		return VerifiedObjectCopy{}, ErrInvalid
	}
	result, err := provider.CopyObjectBetweenBuckets(ctx, sourceBucket, destinationBucket, CopyObjectRequest{
		SourceKey: item.Key, SourceVersion: item.VersionID, SourceMetadataVersion: item.MetadataVersion,
		DestinationKey: item.Key,
	})
	if err != nil {
		return VerifiedObjectCopy{}, err
	}
	source, err := provider.ReadObjectVersion(ctx, sourceBucket, item.Key, item.VersionID)
	if err != nil {
		return VerifiedObjectCopy{}, err
	}
	sourceSum, sourceLength, sourceErr := hashObjectSnapshotStream(source)
	if sourceErr != nil {
		return VerifiedObjectCopy{}, sourceErr
	}
	destination, err := provider.ReadObject(ctx, destinationBucket, item.Key)
	if err != nil {
		return VerifiedObjectCopy{}, err
	}
	destinationSum, destinationLength, destinationErr := hashObjectSnapshotStream(destination)
	if destinationErr != nil {
		return VerifiedObjectCopy{}, destinationErr
	}
	if sourceLength != item.Size || destinationLength != item.Size || sourceSum != destinationSum {
		return VerifiedObjectCopy{}, ErrObjectSnapshotCopyMismatch
	}
	return VerifiedObjectCopy{CopyObjectResult: result, SHA256: hex.EncodeToString(sourceSum[:])}, nil
}

func hashObjectSnapshotStream(stream io.ReadCloser) ([sha256.Size]byte, int64, error) {
	var zero [sha256.Size]byte
	if stream == nil {
		return zero, 0, ErrUnavailable
	}
	h := sha256.New()
	n, copyErr := io.Copy(h, stream)
	closeErr := stream.Close()
	if copyErr != nil {
		return zero, n, copyErr
	}
	if closeErr != nil {
		return zero, n, closeErr
	}
	return hashToSHA256(h), n, nil
}

func hashToSHA256(h hash.Hash) [sha256.Size]byte {
	var out [sha256.Size]byte
	copy(out[:], h.Sum(nil))
	return out
}
