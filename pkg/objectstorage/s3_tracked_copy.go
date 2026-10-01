package objectstorage

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var _ TrackedObjectCopier = (*S3)(nil)
var _ ConditionalTrackedObjectCopier = (*S3)(nil)

func (p *S3) SnapshotCopySource(ctx context.Context, bucket, key string) (CopySourceSnapshot, error) {
	return p.snapshotCopySource(ctx, bucket, key, api.MaxObjectSinglePutBytes)
}

func (p *S3) snapshotCopySource(ctx context.Context, bucket, key string, maxBytes int64) (CopySourceSnapshot, error) {
	if !ValidKey(key) {
		return CopySourceSnapshot{}, ErrInvalid
	}
	out, err := p.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)}, func(o *s3.Options) { o.RetryMaxAttempts = 1 })
	if err != nil {
		return CopySourceSnapshot{}, normalize(err)
	}
	if out == nil || out.ContentLength == nil {
		return CopySourceSnapshot{}, ErrUnavailable
	}
	if v := aws.ToString(out.VersionId); v != "" && v != "null" {
		return CopySourceSnapshot{}, ErrUnsupported
	}
	snapshot := CopySourceSnapshot{SizeBytes: *out.ContentLength, ETag: aws.ToString(out.ETag), Metadata: ObjectMetadata{ContentType: aws.ToString(out.ContentType), CacheControl: aws.ToString(out.CacheControl), ContentDisposition: aws.ToString(out.ContentDisposition), ContentEncoding: aws.ToString(out.ContentEncoding), ContentLanguage: aws.ToString(out.ContentLanguage), Metadata: copyCustomerMetadata(out.Metadata)}}
	if !validCopySourceSize(snapshot, maxBytes) {
		return CopySourceSnapshot{}, ErrInvalid
	}
	if raw := aws.ToString(out.ExpiresString); raw != "" {
		expires, err := http.ParseTime(raw)
		if err != nil {
			return CopySourceSnapshot{}, ErrUnsupported
		}
		snapshot.Expires = &expires
	}
	return snapshot, nil
}

func validCopySource(s CopySourceSnapshot) bool {
	return validCopySourceSize(s, api.MaxObjectSinglePutBytes)
}

func validCopySourceSize(s CopySourceSnapshot, maxBytes int64) bool {
	return s.SizeBytes >= 0 && s.SizeBytes <= maxBytes && s.ETag != "" && s.ETag != "*" && validCopyETagCondition(s.ETag) && ValidateObjectMetadata(s.Metadata) == nil
}

func copyCustomerMetadata(source map[string]string) map[string]string {
	metadata := cloneMetadata(source)
	for key := range metadata {
		if strings.EqualFold(key, ReservedUploadReceiptMetadataKey) || strings.EqualFold(key, ReservedMultipartSessionMetadataKey) || strings.EqualFold(key, ReservedObjectTagsMetadataKey) {
			delete(metadata, key)
		}
	}
	return metadata
}

func (p *S3) CopyTrackedObject(ctx context.Context, bucket, receipt string, r CopyObjectRequest, source CopySourceSnapshot) (CopyObjectResult, error) {
	return p.CopyConditionalTrackedObject(ctx, bucket, receipt, r, source, CopySourceConditions{})
}

func (p *S3) CopyConditionalTrackedObject(ctx context.Context, bucket, receipt string, r CopyObjectRequest, source CopySourceSnapshot, conditions CopySourceConditions) (CopyObjectResult, error) {
	if _, err := uuid.Parse(receipt); err != nil || ctx.Err() != nil || !validCopySource(source) || ValidateObjectMetadata(r.Metadata) != nil {
		return CopyObjectResult{}, errors.Join(ErrWriteRejected, ErrInvalid)
	}
	if err := conditions.Check(source); err != nil {
		return CopyObjectResult{}, errors.Join(ErrWriteRejected, err)
	}
	copyMetadata := r.MetadataDirective == "" || r.MetadataDirective == "COPY"
	if copyMetadata {
		tags := r.Metadata.Tags
		r.Metadata = source.Metadata
		r.Metadata.Tags = tags
		r.MetadataDirective = "REPLACE"
	}
	in, err := copyObjectInput(bucket, bucket, r)
	if err != nil {
		return CopyObjectResult{}, errors.Join(ErrWriteRejected, err)
	}
	in.Metadata = cloneMetadata(in.Metadata)
	if in.Metadata == nil {
		in.Metadata = map[string]string{}
	}
	in.Metadata[ReservedUploadReceiptMetadataKey] = receipt
	in.CopySourceIfMatch = aws.String(source.ETag)
	in.CopySourceIfNoneMatch = stringPtrOrNil(conditions.IfNoneMatch)
	if copyMetadata {
		in.Expires = source.Expires
	}
	out, err := p.client.CopyObject(ctx, in, func(o *s3.Options) { o.RetryMaxAttempts = 1 })
	if err != nil {
		return CopyObjectResult{}, trackedCopyError(err)
	}
	return copyObjectResult(out)
}

func trackedCopyError(err error) error {
	if !definiteS3WriteRejection(err) {
		// Includes embedded HTTP 200 errors and lost/truncated response bodies.
		return ErrUnavailable
	}
	cause := normalize(err)
	var response *smithyhttp.ResponseError
	if errors.As(err, &response) && response.HTTPStatusCode() == http.StatusPreconditionFailed {
		cause = ErrPreconditionFailed
	}
	return errors.Join(ErrWriteRejected, cause)
}
