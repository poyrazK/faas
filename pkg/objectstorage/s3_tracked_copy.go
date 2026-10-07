package objectstorage

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var _ TrackedObjectCopier = (*S3)(nil)
var _ ConditionalTrackedObjectCopier = (*S3)(nil)
var _ DateConditionalTrackedObjectCopier = (*S3)(nil)

func (p *S3) SnapshotCopySource(ctx context.Context, bucket, key string) (CopySourceSnapshot, error) {
	return p.snapshotCopySource(ctx, bucket, key, "", api.MaxObjectSinglePutBytes)
}

func (p *S3) snapshotCopySource(ctx context.Context, bucket, key, version string, maxBytes int64) (CopySourceSnapshot, error) {
	if !ValidKey(key) || version != "" && !validNativeVersionID(version) {
		return CopySourceSnapshot{}, ErrInvalid
	}
	out, err := p.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), VersionId: stringPtrOrNil(version)}, func(o *s3.Options) { o.RetryMaxAttempts = 1 })
	if err != nil {
		return CopySourceSnapshot{}, copySourceHeadError(err, version)
	}
	if out == nil || out.ContentLength == nil {
		return CopySourceSnapshot{}, ErrUnavailable
	}
	if !validCopySnapshotVersion(out.ResultMetadata, aws.ToString(out.VersionId), version) {
		return CopySourceSnapshot{}, ErrUnavailable
	}
	if aws.ToBool(out.DeleteMarker) {
		if version != "" {
			return CopySourceSnapshot{}, ErrInvalid
		}
		return CopySourceSnapshot{}, ErrNotFound
	}
	snapshot := CopySourceSnapshot{SizeBytes: *out.ContentLength, ETag: aws.ToString(out.ETag), Metadata: ObjectMetadata{ContentType: aws.ToString(out.ContentType), CacheControl: aws.ToString(out.CacheControl), ContentDisposition: aws.ToString(out.ContentDisposition), ContentEncoding: aws.ToString(out.ContentEncoding), ContentLanguage: aws.ToString(out.ContentLanguage), Metadata: copyCustomerMetadata(out.Metadata)}}
	snapshot.ProviderVersionID = aws.ToString(out.VersionId)
	if version == "null" && snapshot.ProviderVersionID == "" {
		snapshot.ProviderVersionID = "null"
	}
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
	return s.SizeBytes >= 0 && s.SizeBytes <= maxBytes && s.ETag != "" && s.ETag != "*" && validCopyETagCondition(s.ETag) && (s.ProviderVersionID == "" || validNativeVersionID(s.ProviderVersionID)) && ValidateObjectMetadata(s.Metadata) == nil
}

func immutableCopySource(s CopySourceSnapshot) bool {
	return s.ProviderVersionID != "" && s.ProviderVersionID != "null"
}

func trackedCopySource(bucket, key string, s CopySourceSnapshot) string {
	value := url.PathEscape(bucket + "/" + key)
	if s.ProviderVersionID != "" {
		value += "?versionId=" + url.QueryEscape(s.ProviderVersionID)
	}
	return value
}

func trackedCopySourceMatch(s CopySourceSnapshot, c CopySourceConditions) *string {
	if immutableCopySource(s) && c.HasDates() && c.IfMatch == "" {
		// The version is the identity fence. Adding If-Match here would make
		// S3 ignore an independently restrictive If-Unmodified-Since.
		return nil
	}
	return aws.String(s.ETag)
}

func copyCustomerMetadata(source map[string]string) map[string]string {
	metadata := cloneMetadata(source)
	for key := range metadata {
		if strings.EqualFold(key, ReservedUploadReceiptMetadataKey) || strings.EqualFold(key, ReservedMultipartSessionMetadataKey) || strings.EqualFold(key, ReservedObjectTagsMetadataKey) || strings.EqualFold(key, ReservedObjectProtectionMetadataKey) || strings.EqualFold(key, ReservedObjectEncryptionMetadataKey) {
			delete(metadata, key)
		}
	}
	return metadata
}

func (p *S3) CopyTrackedObject(ctx context.Context, bucket, receipt string, r CopyObjectRequest, source CopySourceSnapshot) (CopyObjectResult, error) {
	return p.CopyConditionalTrackedObject(ctx, bucket, receipt, r, source, CopySourceConditions{})
}

func (p *S3) CopyDateConditionalTrackedObject(ctx context.Context, bucket, receipt string, r CopyObjectRequest, source CopySourceSnapshot, conditions CopySourceConditions) (CopyObjectResult, error) {
	return p.CopyConditionalTrackedObject(ctx, bucket, receipt, r, source, conditions)
}

func (p *S3) CopyConditionalTrackedObject(ctx context.Context, bucket, receipt string, r CopyObjectRequest, source CopySourceSnapshot, conditions CopySourceConditions) (CopyObjectResult, error) {
	return p.copyEncryptedObject(ctx, bucket, bucket, receipt, r, source, conditions, nil)
}

func (p *S3) copyEncryptedObject(ctx context.Context, sourceBucket, bucket, receipt string, r CopyObjectRequest, source CopySourceSnapshot, conditions CopySourceConditions, encryption *ResolvedObjectEncryption) (CopyObjectResult, error) {
	if _, err := uuid.Parse(receipt); err != nil || ctx.Err() != nil || !validCopySource(source) || ValidateObjectMetadata(r.Metadata) != nil || r.SourceProviderVersionID != "" && r.SourceProviderVersionID != source.ProviderVersionID {
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
	in, err := copyObjectInput(sourceBucket, bucket, r)
	if err != nil {
		return CopyObjectResult{}, errors.Join(ErrWriteRejected, err)
	}
	in.Metadata = cloneMetadata(in.Metadata)
	if in.Metadata == nil {
		in.Metadata = map[string]string{}
	}
	in.Metadata[ReservedUploadReceiptMetadataKey] = receipt
	applyCopyEncryption(in, encryption)
	applyCopyProtection(ctx, in)
	in.CopySource = aws.String(trackedCopySource(sourceBucket, r.SourceKey, source))
	in.CopySourceIfMatch = trackedCopySourceMatch(source, conditions)
	in.CopySourceIfNoneMatch = stringPtrOrNil(conditions.IfNoneMatch)
	in.CopySourceIfModifiedSince = conditions.IfModifiedSince
	in.CopySourceIfUnmodifiedSince = conditions.IfUnmodifiedSince
	if copyMetadata {
		in.Expires = source.Expires
	}
	if encryption != nil {
		if err := beforeEncryptionWrite(ctx); err != nil {
			return CopyObjectResult{}, errors.Join(ErrWriteRejected, err)
		}
	}
	out, err := p.client.CopyObject(ctx, in, func(o *s3.Options) { o.RetryMaxAttempts = 1 })
	if err != nil {
		return CopyObjectResult{}, trackedCopyError(err)
	}
	if out != nil && !validCopyResponseSource(out.ResultMetadata, aws.ToString(out.CopySourceVersionId), source.ProviderVersionID) {
		return CopyObjectResult{}, ErrUnavailable
	}
	if out != nil && !validEncryptionResponse(out.ResultMetadata, encryption) {
		return CopyObjectResult{}, ErrUnavailable
	}
	result, err := copyObjectResult(out)
	if err == nil {
		result.Encryption = publicObjectEncryption(encryption)
		if !capturedWriteProtection(ctx).Empty() {
			result.VerifiedProtection, err = p.ConfirmObjectWriteProtection(ctx, bucket, r.DestinationKey, result.ProviderVersionID, receipt, source.SizeBytes, false, result.ETag)
		}
	}
	return result, err
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
