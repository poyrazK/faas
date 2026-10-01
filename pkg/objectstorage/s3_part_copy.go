package objectstorage

import (
	"context"
	"errors"
	"net/url"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/onebox-faas/faas/pkg/api"
)

var _ MultipartPartCopier = (*S3)(nil)

func (p *S3) SnapshotMultipartCopySource(ctx context.Context, bucket, key string) (CopySourceSnapshot, error) {
	return p.snapshotCopySource(ctx, bucket, key, api.MaxObjectUploadBytes)
}

func (p *S3) CopyMultipartPart(ctx context.Context, bucket string, r MultipartPartCopyRequest, source CopySourceSnapshot) (CopyObjectResult, error) {
	if bucket == "" || !ValidKey(r.SourceKey) || !ValidKey(r.Key) || r.ProviderUploadID == "" || r.PartNumber < 1 || r.PartNumber > api.MaxMultipartParts || ctx.Err() != nil || !validCopySourceSize(source, api.MaxObjectUploadBytes) {
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
		PartNumber: aws.Int32(r.PartNumber), CopySource: aws.String(url.PathEscape(bucket + "/" + r.SourceKey)),
		CopySourceIfMatch: aws.String(source.ETag), CopySourceIfNoneMatch: stringPtrOrNil(r.Conditions.IfNoneMatch),
		CopySourceRange: stringPtrOrNil(r.Range.String()),
	}, func(o *s3.Options) { o.RetryMaxAttempts = 1 })
	if err != nil {
		return CopyObjectResult{}, trackedCopyError(err)
	}
	if out == nil || out.CopyPartResult == nil || !validUploadETag(aws.ToString(out.CopyPartResult.ETag)) {
		return CopyObjectResult{}, ErrUnavailable
	}
	return CopyObjectResult{ETag: aws.ToString(out.CopyPartResult.ETag), LastModified: aws.ToTime(out.CopyPartResult.LastModified)}, nil
}
