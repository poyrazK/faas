package objectstorage

import (
	"context"
	"errors"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectchecksum"
)

var _ MultipartPartCopier = (*S3)(nil)
var _ DateConditionalMultipartPartCopier = (*S3)(nil)

func (p *S3) SnapshotMultipartCopySource(ctx context.Context, bucket, key string) (CopySourceSnapshot, error) {
	return p.snapshotCopySource(ctx, bucket, key, "", api.MaxObjectUploadBytes)
}

func (p *S3) CopyDateConditionalMultipartPart(ctx context.Context, bucket string, r MultipartPartCopyRequest, source CopySourceSnapshot) (CopyObjectResult, error) {
	return p.CopyMultipartPart(ctx, bucket, r, source)
}

func (p *S3) CopyMultipartPart(ctx context.Context, bucket string, r MultipartPartCopyRequest, source CopySourceSnapshot) (CopyObjectResult, error) {
	return p.copyMultipartPart(ctx, bucket, bucket, r, source)
}
func (p *S3) copyMultipartPart(ctx context.Context, sourceBucket, bucket string, r MultipartPartCopyRequest, source CopySourceSnapshot) (CopyObjectResult, error) {
	if !r.Checksum.Valid() || bucket == "" || !ValidKey(r.SourceKey) || !ValidKey(r.Key) || r.ProviderUploadID == "" || r.PartNumber < 1 || r.PartNumber > api.MaxMultipartParts || ctx.Err() != nil || !validCopySourceSize(source, api.MaxObjectUploadBytes) || r.SourceProviderVersionID != "" && r.SourceProviderVersionID != source.ProviderVersionID {
		return CopyObjectResult{}, errors.Join(ErrWriteRejected, ErrInvalid)
	}
	if _, err := MultipartCopySize(source, r.Range); err != nil {
		return CopyObjectResult{}, errors.Join(ErrWriteRejected, err)
	}
	if err := r.Conditions.Check(source); err != nil {
		return CopyObjectResult{}, errors.Join(ErrWriteRejected, err)
	}
	out, err := p.client.UploadPartCopy(ctx, &s3.UploadPartCopyInput{
		Bucket: aws.String(bucket), Key: aws.String(r.Key), UploadId: aws.String(r.ProviderUploadID),
		PartNumber: aws.Int32(r.PartNumber), CopySource: aws.String(trackedCopySource(sourceBucket, r.SourceKey, source)),
		CopySourceIfMatch: trackedCopySourceMatch(source, r.Conditions), CopySourceIfNoneMatch: stringPtrOrNil(r.Conditions.IfNoneMatch),
		CopySourceIfModifiedSince: r.Conditions.IfModifiedSince, CopySourceIfUnmodifiedSince: r.Conditions.IfUnmodifiedSince,
		CopySourceRange: stringPtrOrNil(r.Range.String()),
	}, func(o *s3.Options) { o.RetryMaxAttempts = 1 })
	if err != nil {
		return CopyObjectResult{}, trackedCopyError(err)
	}
	if out == nil || out.CopyPartResult == nil || !validUploadETag(aws.ToString(out.CopyPartResult.ETag)) {
		return CopyObjectResult{}, ErrUnavailable
	}
	if !validCopyResponseSource(out.ResultMetadata, aws.ToString(out.CopySourceVersionId), source.ProviderVersionID) {
		return CopyObjectResult{}, ErrUnavailable
	}
	d := objectchecksum.Digest{Policy: r.Checksum}
	if !r.Checksum.Empty() {
		c := out.CopyPartResult
		d.Value = checksumValue(r.Checksum.Algorithm, c.ChecksumCRC32, c.ChecksumCRC32C, c.ChecksumCRC64NVME, c.ChecksumSHA1, c.ChecksumSHA256)
		if !r.Checksum.Valid() || !objectchecksum.ValidPart(r.Checksum.Algorithm, d.Value) {
			return CopyObjectResult{}, ErrUnavailable
		}
	}
	return CopyObjectResult{Checksum: d, ETag: aws.ToString(out.CopyPartResult.ETag), LastModified: aws.ToTime(out.CopyPartResult.LastModified)}, nil
}
