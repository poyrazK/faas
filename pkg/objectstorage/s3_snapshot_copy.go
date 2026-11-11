package objectstorage

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var _ RecoverableSnapshotCopier = (*S3)(nil)
var _ SnapshotCopyValidator = (*S3)(nil)

// SnapshotCopySizeError identifies a selected object the native single-copy
// operation cannot copy. It is a permanent qualification failure, not drift.
type SnapshotCopySizeError struct {
	Key                   string
	LimitBytes, SizeBytes int64
}

func (e *SnapshotCopySizeError) Error() string {
	return fmt.Sprintf("snapshot object %q exceeds the native copy limit: limit %d bytes, observed %d; see %s", e.Key, e.LimitBytes, e.SizeBytes, api.ObjectStorageLimitsDocsURL)
}
func (*SnapshotCopySizeError) Unwrap() error { return ErrUnsupported }

func (*S3) ValidateSnapshotCopy(item ObjectVersion) error {
	if !ValidKey(item.Key) || !validNativeVersionID(item.VersionID) || item.Size < 0 || item.Deleted || item.MetadataVersion != "" {
		return ErrInvalid
	}
	if item.Size > api.MaxObjectSinglePutBytes {
		return &SnapshotCopySizeError{Key: item.Key, LimitBytes: api.MaxObjectSinglePutBytes, SizeBytes: item.Size}
	}
	return nil
}

func (p *S3) CopySnapshotObject(ctx context.Context, sourceBucket, destinationBucket string, item ObjectVersion) (CopyObjectResult, error) {
	if sourceBucket == "" || destinationBucket == "" || sourceBucket == destinationBucket {
		return CopyObjectResult{}, ErrInvalid
	}
	if err := p.ValidateSnapshotCopy(item); err != nil {
		return CopyObjectResult{}, err
	}
	hash, err := ObjectManifestHash([]ObjectVersion{item})
	if err != nil {
		return CopyObjectResult{}, err
	}
	// The target bucket belongs exclusively to this clone. The receipt binds
	// its exact source manifest; customer metadata cannot set this reserved key.
	receipt := uuid.NewSHA1(uuid.NameSpaceURL, []byte("gregale/snapshot-copy/v1\x00"+sourceBucket+"\x00"+destinationBucket+"\x00"+hash)).String()
	if result, found, err := p.snapshotCopyProof(ctx, destinationBucket, item, receipt); found || err != nil {
		return result, err
	}
	source, err := p.snapshotCopySource(ctx, sourceBucket, item.Key, item.VersionID, api.MaxObjectSinglePutBytes)
	if err != nil {
		return CopyObjectResult{}, err
	}
	if source.SizeBytes != item.Size || item.ETag != "" && item.ETag != source.ETag {
		return CopyObjectResult{}, ErrObjectSnapshotCopyMismatch
	}
	in, err := copyObjectInput(sourceBucket, destinationBucket, CopyObjectRequest{SourceKey: item.Key, SourceVersion: item.VersionID, DestinationKey: item.Key, MetadataDirective: "REPLACE", Metadata: source.Metadata})
	if err != nil {
		return CopyObjectResult{}, err
	}
	in.Metadata = cloneMetadata(in.Metadata)
	if in.Metadata == nil {
		in.Metadata = map[string]string{}
	}
	in.Metadata[ReservedUploadReceiptMetadataKey] = receipt
	in.Expires, in.CopySourceIfMatch, in.IfNoneMatch = source.Expires, aws.String(source.ETag), aws.String("*")
	// One dispatch, and native create-only semantics even if an earlier
	// request completes after its caller's deadline. A retry may only adopt it.
	_, copyErr := p.client.CopyObject(ctx, in, func(o *s3.Options) { o.RetryMaxAttempts = 1 })
	proofCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.ObjectUploadSettlementTimeout)
	defer cancel()
	result, found, proofErr := p.snapshotCopyProof(proofCtx, destinationBucket, item, receipt)
	if proofErr != nil || found {
		return result, proofErr
	}
	if copyErr != nil {
		return CopyObjectResult{}, trackedCopyError(copyErr)
	}
	return CopyObjectResult{}, ErrUnavailable
}

func (p *S3) snapshotCopyProof(ctx context.Context, bucket string, item ObjectVersion, receipt string) (CopyObjectResult, bool, error) {
	out, err := p.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(item.Key)}, func(o *s3.Options) { o.RetryMaxAttempts = 1 })
	if errors.Is(normalizeVersionHistoryError(err), ErrNotFound) {
		return CopyObjectResult{}, false, nil
	}
	if err != nil {
		return CopyObjectResult{}, false, normalizeVersionHistoryError(err)
	}
	if out == nil || out.ContentLength == nil || aws.ToBool(out.DeleteMarker) || !validTrackedProofHeaders(out.ResultMetadata, ReservedUploadReceiptMetadataKey) || !validCopySnapshotVersion(out.ResultMetadata, aws.ToString(out.VersionId), "") || out.VersionId == nil || *out.VersionId == "null" || !validNativeVersionID(*out.VersionId) || !validUploadETag(aws.ToString(out.ETag)) {
		return CopyObjectResult{}, false, ErrUnavailable
	}
	if *out.ContentLength != item.Size || out.Metadata[ReservedUploadReceiptMetadataKey] != receipt {
		return CopyObjectResult{}, false, ErrConflict
	}
	return CopyObjectResult{ETag: aws.ToString(out.ETag), ProviderVersionID: *out.VersionId, LastModified: aws.ToTime(out.LastModified)}, true, nil
}
