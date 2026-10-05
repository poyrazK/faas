package objectstorage

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/onebox-faas/faas/pkg/state"
)

var _ ObjectWriteProtectionProvider = (*S3)(nil)

func (p *S3) BindObjectWriteProtection(ctx context.Context, snapshot state.ObjectWriteProtectionSnapshot) (context.Context, error) {
	if !snapshot.Valid() || snapshot.Empty() {
		return ctx, ErrInvalid
	}
	return context.WithValue(ctx, writeProtectionKey{}, snapshot.Clone()), nil
}
func protectionMetadata(ctx context.Context, metadata map[string]string) map[string]string {
	p := capturedWriteProtection(ctx)
	if p.Empty() {
		return metadata
	}
	metadata = cloneMetadata(metadata)
	if metadata == nil {
		metadata = map[string]string{}
	}
	metadata[ReservedObjectProtectionMetadataKey] = p.Proof()
	return metadata
}
func applyPutProtection(ctx context.Context, in *s3.PutObjectInput) {
	p := capturedWriteProtection(ctx)
	if p.Empty() {
		return
	}
	in.Metadata = protectionMetadata(ctx, in.Metadata)
	if r := p.Requested.Retention; r != nil {
		in.ObjectLockMode = types.ObjectLockMode(r.Mode)
		in.ObjectLockRetainUntilDate = r.RetainUntilDate
	}
	if h := p.Requested.LegalHold; h != nil {
		in.ObjectLockLegalHoldStatus = types.ObjectLockLegalHoldStatus(h.Status)
	}
	if checksum, ok := ctx.Value(writeChecksumKey{}).(string); ok {
		in.ContentMD5 = aws.String(checksum)
	} else if in.Body != nil {
		in.ChecksumAlgorithm = types.ChecksumAlgorithmCrc32
	}
}
func applyCopyProtection(ctx context.Context, in *s3.CopyObjectInput) {
	p := capturedWriteProtection(ctx)
	if p.Empty() {
		return
	}
	in.Metadata = protectionMetadata(ctx, in.Metadata)
	if r := p.Requested.Retention; r != nil {
		in.ObjectLockMode = types.ObjectLockMode(r.Mode)
		in.ObjectLockRetainUntilDate = r.RetainUntilDate
	}
	if h := p.Requested.LegalHold; h != nil {
		in.ObjectLockLegalHoldStatus = types.ObjectLockLegalHoldStatus(h.Status)
	}
}
func applyMultipartProtection(ctx context.Context, in *s3.CreateMultipartUploadInput) {
	p := capturedWriteProtection(ctx)
	if p.Empty() {
		return
	}
	in.Metadata = protectionMetadata(ctx, in.Metadata)
	if r := p.Requested.Retention; r != nil {
		in.ObjectLockMode = types.ObjectLockMode(r.Mode)
		in.ObjectLockRetainUntilDate = r.RetainUntilDate
	}
	if h := p.Requested.LegalHold; h != nil {
		in.ObjectLockLegalHoldStatus = types.ObjectLockLegalHoldStatus(h.Status)
	}
}
func validProtectedHead(ctx context.Context, out *s3.HeadObjectOutput) bool {
	p := capturedWriteProtection(ctx)
	if p.Empty() {
		return true
	}
	if out == nil || !p.Valid() || aws.ToString(out.VersionId) == "" || aws.ToString(out.VersionId) == "null" || out.Metadata[ReservedObjectProtectionMetadataKey] != p.Proof() {
		return false
	}
	raw, ok := awsmiddleware.GetRawResponse(out.ResultMetadata).(*smithyhttp.Response)
	if !ok || raw == nil || raw.Response == nil || !oneEncryptionHeader(raw.Header, "X-Amz-Meta-"+ReservedObjectProtectionMetadataKey, p.Proof()) {
		return false
	}
	headers := raw.Header
	for name := range headers {
		if strings.HasPrefix(strings.ToLower(name), "x-amz-object-lock-") && !strings.EqualFold(name, "X-Amz-Object-Lock-Mode") && !strings.EqualFold(name, "X-Amz-Object-Lock-Retain-Until-Date") && !strings.EqualFold(name, "X-Amz-Object-Lock-Legal-Hold") {
			return false
		}
	}
	mode := encryptionHeaderValues(headers, "X-Amz-Object-Lock-Mode")
	dates := encryptionHeaderValues(headers, "X-Amz-Object-Lock-Retain-Until-Date")
	holds := encryptionHeaderValues(headers, "X-Amz-Object-Lock-Legal-Hold")
	if len(mode) > 1 || len(dates) > 1 || len(holds) > 1 || len(mode) != len(dates) || len(holds) == 1 && holds[0] != "ON" && holds[0] != "OFF" {
		return false
	}
	if len(mode) == 1 {
		if mode[0] != "GOVERNANCE" && mode[0] != "COMPLIANCE" {
			return false
		}
		until, err := time.Parse(time.RFC3339Nano, dates[0])
		if err != nil || until.IsZero() || until.Year() > 9999 || out.ObjectLockRetainUntilDate == nil || !out.ObjectLockRetainUntilDate.Equal(until) || string(out.ObjectLockMode) != mode[0] {
			return false
		}
	}
	wanted := p.MinimumRetention()
	if !wanted.Empty() && (len(mode) != 1 || mode[0] != wanted.Mode || out.ObjectLockRetainUntilDate.Before(*wanted.RetainUntilDate)) {
		return false
	}
	if h := p.Requested.LegalHold; h != nil && (len(holds) != 1 || holds[0] != h.Status) {
		return false
	}
	return true
}

// Native acknowledgments do not prove stored Object Lock policy. Read the exact
// created version with its private write/session receipt before settlement.
func (p *S3) ConfirmObjectWriteProtection(ctx context.Context, bucket, key, version, receipt string, size int64, multipart bool, etag string) (string, error) {
	snapshot := capturedWriteProtection(ctx)
	if snapshot.Empty() || !validNativeVersionID(version) || version == "null" || !ValidKey(key) {
		return "", ErrUnavailable
	}
	if err := beforeProtectionRead(ctx); err != nil {
		return "", err
	}
	out, err := p.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), VersionId: aws.String(version)}, func(o *s3.Options) { o.RetryMaxAttempts = 1 })
	if err != nil {
		return "", normalizeVersionHistoryError(err)
	}
	metadataKey := ReservedUploadReceiptMetadataKey
	if multipart {
		metadataKey = ReservedMultipartSessionMetadataKey
	}
	if out == nil || out.ContentLength == nil || *out.ContentLength != size || out.Metadata[metadataKey] != receipt || aws.ToBool(out.DeleteMarker) || !validUploadETag(aws.ToString(out.ETag)) || aws.ToString(out.ETag) != etag || !validTrackedProofHeaders(out.ResultMetadata, metadataKey) || !validCopySnapshotVersion(out.ResultMetadata, aws.ToString(out.VersionId), version) || !validProtectedHead(ctx, out) {
		return "", ErrUnavailable
	}
	return snapshot.Proof(), nil
}
func validProtectedSignedPut(ctx context.Context, headers http.Header, signed string) bool {
	p := capturedWriteProtection(ctx)
	if p.Empty() {
		return true
	}
	for _, name := range []string{"X-Amz-Meta-" + ReservedObjectProtectionMetadataKey, "Content-Md5", "X-Amz-Object-Lock-Mode", "X-Amz-Object-Lock-Retain-Until-Date", "X-Amz-Object-Lock-Legal-Hold"} {
		values := encryptionHeaderValues(headers, name)
		if len(values) > 1 || len(values) == 1 && !strings.Contains(";"+signed+";", ";"+strings.ToLower(name)+";") {
			return false
		}
	}
	checksum, _ := ctx.Value(writeChecksumKey{}).(string)
	if r := p.Requested.Retention; r != nil {
		if !oneEncryptionHeader(headers, "X-Amz-Object-Lock-Mode", r.Mode) || !oneEncryptionHeader(headers, "X-Amz-Object-Lock-Retain-Until-Date", r.RetainUntilDate.UTC().Format(time.RFC3339Nano)) {
			return false
		}
	}
	if h := p.Requested.LegalHold; h != nil && !oneEncryptionHeader(headers, "X-Amz-Object-Lock-Legal-Hold", h.Status) {
		return false
	}
	return checksum != "" && oneEncryptionHeader(headers, "Content-Md5", checksum) && oneEncryptionHeader(headers, "X-Amz-Meta-"+ReservedObjectProtectionMetadataKey, p.Proof())
}
