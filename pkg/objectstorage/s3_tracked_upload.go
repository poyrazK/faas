package objectstorage

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var _ TrackedObjectWriter = (*S3)(nil)

func validUploadETag(etag string) bool {
	return strings.TrimSpace(etag) != "" && len(etag) <= api.MaxObjectWriteETagBytes && !strings.ContainsAny(etag, "\r\n")
}
func (p *S3) WriteTrackedObject(ctx context.Context, bucket, key, receipt string, body io.Reader, size int64, metadata ObjectMetadata) (UploadResult, error) {
	if ctx.Err() != nil {
		return UploadResult{}, ErrWriteRejected
	}
	if _, err := uuid.Parse(receipt); err != nil {
		return UploadResult{}, ErrWriteRejected
	}
	if !ValidKey(key) || size < 0 || size > api.MaxObjectSinglePutBytes || ValidateObjectMetadata(metadata) != nil {
		return UploadResult{}, ErrWriteRejected
	}
	return p.writeObject(ctx, bucket, key, body, size, metadata, receipt)
}
func definiteS3WriteRejection(err error) bool {
	var response *smithyhttp.ResponseError
	// Service rejection is authoritative for this single attempt. Transport
	// errors, timeouts, 5xx and missing acknowledgments remain uncertain.
	return errors.As(err, &response) && response.HTTPStatusCode() >= 400 && response.HTTPStatusCode() < 500 && response.HTTPStatusCode() != http.StatusRequestTimeout
}
func (p *S3) ConfirmTrackedObject(ctx context.Context, bucket, key, receipt string, size int64) (UploadResult, error) {
	if _, err := uuid.Parse(receipt); err != nil || !ValidKey(key) || size < 0 || size > api.MaxObjectSinglePutBytes {
		return UploadResult{}, ErrInvalid
	}
	out, err := p.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)}, func(o *s3.Options) { o.RetryMaxAttempts = 1 })
	if err != nil {
		return UploadResult{}, normalize(err)
	}
	if out == nil || out.ContentLength == nil || *out.ContentLength != size || out.Metadata[ReservedUploadReceiptMetadataKey] != receipt || !validUploadETag(aws.ToString(out.ETag)) {
		return UploadResult{}, ErrConflict
	}
	return UploadResult{ETag: aws.ToString(out.ETag), ProviderVersionID: aws.ToString(out.VersionId)}, nil
}

func invalidS3Write(receipt string) error {
	if receipt != "" {
		return ErrWriteRejected
	}
	return ErrInvalid
}

var _ TrackedObjectPresigner = (*S3)(nil)

func (p *S3) PresignTrackedPut(ctx context.Context, bucket string, r SignRequest, c ObjectWriteConditions, receipt string) (SignedRequest, error) {
	if _, err := uuid.Parse(receipt); err != nil || r.Method != http.MethodPut || !c.Valid() {
		return SignedRequest{}, ErrInvalid
	}
	return p.presign(ctx, bucket, r, c, receipt)
}
