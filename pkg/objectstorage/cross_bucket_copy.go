package objectstorage

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// CrossBucketTrackedObjectCopier explicitly opts into owned source/destination
// placement and preserves immutable source selection, conditions and encryption.
type CrossBucketTrackedObjectCopier interface {
	DateConditionalTrackedObjectCopier
	VersionedTrackedObjectCopier
	CopyCrossBucketTrackedObject(context.Context, string, string, string, CopyObjectRequest, CopySourceSnapshot, CopySourceConditions, ResolvedObjectEncryption) (CopyObjectResult, error)
}

var _ CrossBucketTrackedObjectCopier = (*S3)(nil)

func (p *S3) CopyCrossBucketTrackedObject(ctx context.Context, sourceBucket, destinationBucket, receipt string, r CopyObjectRequest, source CopySourceSnapshot, c CopySourceConditions, e ResolvedObjectEncryption) (CopyObjectResult, error) {
	if sourceBucket == "" || destinationBucket == "" || sourceBucket == destinationBucket {
		return CopyObjectResult{}, errors.Join(ErrWriteRejected, ErrInvalid)
	}
	if _, err := uuid.Parse(receipt); err != nil || ctx.Err() != nil || !validCopySource(source) || ValidateObjectMetadata(r.Metadata) != nil || !ValidKey(r.SourceKey) || !ValidKey(r.DestinationKey) || r.SourceProviderVersionID != "" && r.SourceProviderVersionID != source.ProviderVersionID {
		return CopyObjectResult{}, errors.Join(ErrWriteRejected, ErrInvalid)
	}
	if err := c.Check(source); err != nil {
		return CopyObjectResult{}, errors.Join(ErrWriteRejected, err)
	}
	if e.Empty() {
		return p.copyEncryptedObject(ctx, sourceBucket, destinationBucket, receipt, r, source, c, nil)
	}
	if err := p.CheckEncryptionKey(ctx, e); err != nil {
		return CopyObjectResult{}, errors.Join(ErrWriteRejected, err)
	}
	return p.copyEncryptedObject(ctx, sourceBucket, destinationBucket, receipt, r, source, c, &e)
}

// CrossBucketMultipartPartCopier preserves the selected source across native
// part copy while the destination session owns encryption and completion.
type CrossBucketMultipartPartCopier interface {
	DateConditionalMultipartPartCopier
	VersionedMultipartPartCopier
	CopyCrossBucketMultipartPart(context.Context, string, string, MultipartPartCopyRequest, CopySourceSnapshot) (CopyObjectResult, error)
}

var _ CrossBucketMultipartPartCopier = (*S3)(nil)

func (p *S3) CopyCrossBucketMultipartPart(ctx context.Context, sourceBucket, destinationBucket string, r MultipartPartCopyRequest, s CopySourceSnapshot) (CopyObjectResult, error) {
	if sourceBucket == "" || sourceBucket == destinationBucket {
		return CopyObjectResult{}, errors.Join(ErrWriteRejected, ErrInvalid)
	}
	return p.copyMultipartPart(ctx, sourceBucket, destinationBucket, r, s)
}
