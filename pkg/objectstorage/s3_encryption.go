package objectstorage

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var _ ObjectEncryptionProvider = (*S3)(nil)

func (p *S3) verifyEncryption(e ResolvedObjectEncryption) error {
	if e.Selection.Empty() {
		return ErrInvalid
	}
	return p.encryption.VerifySnapshot(e.AccountID, e)
}

// DescribeKey validates immutable identity, enabled state and key type. It
// cannot establish S3's GenerateDataKey/Decrypt permissions: those are enforced
// by the native write/read, and must not be replaced with a successful probe.
func (p *S3) CheckEncryptionKey(ctx context.Context, e ResolvedObjectEncryption) error {
	if err := p.verifyEncryption(e); err != nil {
		return err
	}
	if e.Selection.Algorithm == "AES256" {
		return ctx.Err()
	}
	if p.kms == nil {
		return ErrConfiguration
	}
	if err := beforeEncryptionKeyRequest(ctx); err != nil {
		return err
	}
	out, err := p.kms.DescribeKey(ctx, &kms.DescribeKeyInput{KeyId: aws.String(e.ProviderKeyID)})
	if err != nil {
		return normalizeEncryptionKeyError(err)
	}
	if out == nil || out.KeyMetadata == nil {
		return ErrUnavailable
	}
	key := out.KeyMetadata
	parts := strings.Split(e.ProviderKeyID, ":")
	if len(parts) != 6 || aws.ToString(key.Arn) != e.ProviderKeyID || aws.ToString(key.AWSAccountId) != parts[4] || aws.ToString(key.KeyId) != strings.TrimPrefix(parts[5], "key/") || !key.Enabled || key.KeyState != kmstypes.KeyStateEnabled || key.KeyUsage != kmstypes.KeyUsageTypeEncryptDecrypt || key.KeySpec != kmstypes.KeySpecSymmetricDefault || key.KeyManager != kmstypes.KeyManagerTypeCustomer {
		return ErrConfiguration
	}
	return nil
}

func normalizeEncryptionKeyError(err error) error {
	if err == nil {
		return nil
	}
	var service smithy.APIError
	if errors.As(err, &service) {
		switch service.ErrorCode() {
		case "AccessDeniedException", "NotFoundException", "InvalidArnException", "DisabledException", "KMSInvalidStateException", "InvalidKeyUsageException", "UnrecognizedClientException", "ExpiredTokenException":
			return ErrConfiguration
		}
	}
	return ErrUnavailable
}

func (p *S3) WriteEncryptedObject(ctx context.Context, bucket, key, receipt string, body io.Reader, size int64, metadata ObjectMetadata, e ResolvedObjectEncryption) (UploadResult, error) {
	if _, err := uuid.Parse(receipt); err != nil || !ValidKey(key) || size < 0 || size > api.MaxObjectSinglePutBytes || ValidateObjectMetadata(metadata) != nil {
		return UploadResult{}, errors.Join(ErrWriteRejected, ErrInvalid)
	}
	if err := p.CheckEncryptionKey(ctx, e); err != nil {
		return UploadResult{}, errors.Join(ErrWriteRejected, err)
	}
	return p.writeObjectEncrypted(ctx, bucket, key, body, size, metadata, receipt, &e)
}

func (p *S3) PresignEncryptedPut(ctx context.Context, bucket string, r SignRequest, c ObjectWriteConditions, receipt string, e ResolvedObjectEncryption) (SignedRequest, error) {
	if _, err := uuid.Parse(receipt); err != nil || r.Method != http.MethodPut || r.Validate(api.MaxObjectSinglePutBytes) != nil || !c.Valid() {
		return SignedRequest{}, ErrInvalid
	}
	if err := p.CheckEncryptionKey(ctx, e); err != nil {
		return SignedRequest{}, err
	}
	return p.presignEncrypted(ctx, bucket, r, c, receipt, &e)
}

func (p *S3) CopyEncryptedObject(ctx context.Context, bucket, receipt string, r CopyObjectRequest, source CopySourceSnapshot, c CopySourceConditions, e ResolvedObjectEncryption) (CopyObjectResult, error) {
	if _, err := uuid.Parse(receipt); err != nil || !validCopySource(source) || ValidateObjectMetadata(r.Metadata) != nil {
		return CopyObjectResult{}, errors.Join(ErrWriteRejected, ErrInvalid)
	}
	if err := c.Check(source); err != nil {
		return CopyObjectResult{}, errors.Join(ErrWriteRejected, err)
	}
	if err := p.CheckEncryptionKey(ctx, e); err != nil {
		return CopyObjectResult{}, errors.Join(ErrWriteRejected, err)
	}
	return p.copyEncryptedObject(ctx, bucket, receipt, r, source, c, &e)
}

func (p *S3) EnsureEncryptedMultipart(ctx context.Context, bucket string, r MultipartCreateRequest, e ResolvedObjectEncryption) (string, error) {
	if _, err := uuid.Parse(r.SessionID); err != nil || !ValidKey(r.Key) || r.SizeBytes < 0 || r.SizeBytes > api.MaxObjectUploadBytes || ValidateObjectMetadata(r.Metadata) != nil {
		return "", ErrInvalid
	}
	if err := p.verifyEncryption(e); err != nil {
		return "", err
	}
	return p.ensureMultipartEncrypted(ctx, bucket, r, &e)
}

func (p *S3) CompleteEncryptedMultipart(ctx context.Context, bucket string, r MultipartCompleteRequest, c ObjectWriteConditions, e ResolvedObjectEncryption) (MultipartCompletionResult, error) {
	if err := p.verifyEncryption(e); err != nil {
		return MultipartCompletionResult{RecoveryCursor: r.RecoveryCursor}, err
	}
	r.Encryption = &e
	return p.CompleteMultipartWithResult(ctx, bucket, r, c)
}

func (p *S3) ConfirmEncryptedObject(ctx context.Context, bucket, key, receipt string, size int64, e ResolvedObjectEncryption) (UploadResult, error) {
	if err := p.verifyEncryption(e); err != nil {
		return UploadResult{}, err
	}
	return p.confirmTrackedObjectEncrypted(ctx, bucket, key, receipt, size, &e)
}

func (p *S3) ConfirmEncryptedObjectHistory(ctx context.Context, bucket string, r ObjectHistoryProofRequest, e ResolvedObjectEncryption) (ObjectHistoryProofPage, error) {
	if err := p.verifyEncryption(e); err != nil {
		return ObjectHistoryProofPage{Cursor: r.Cursor}, err
	}
	r.Encryption = &e
	return p.ConfirmTrackedObjectHistory(ctx, bucket, r)
}

func publicObjectEncryption(e *ResolvedObjectEncryption) api.ObjectEncryption {
	if e == nil {
		return api.ObjectEncryption{}
	}
	return cloneObjectEncryption(e.Selection)
}

func encryptionProofMetadata(metadata map[string]string, e *ResolvedObjectEncryption) map[string]string {
	if e == nil {
		return metadata
	}
	metadata = cloneMetadata(metadata)
	if metadata == nil {
		metadata = map[string]string{}
	}
	metadata[ReservedObjectEncryptionMetadataKey] = e.Proof()
	return metadata
}

func validStoredEncryptionProof(metadata map[string]string, e *ResolvedObjectEncryption) bool {
	return e == nil || metadata[ReservedObjectEncryptionMetadataKey] == e.Proof()
}

func validStoredEncryptionResponse(metadata map[string]string, result middleware.Metadata, e *ResolvedObjectEncryption) bool {
	if e == nil {
		return true
	}
	response, ok := awsmiddleware.GetRawResponse(result).(*smithyhttp.Response)
	return ok && response != nil && response.Response != nil && validStoredEncryptionProof(metadata, e) && oneEncryptionHeader(response.Header, "X-Amz-Meta-"+ReservedObjectEncryptionMetadataKey, e.Proof())
}

// Completion does not return the encryption context or private proof. Inspect
// its exact native version before accepting it as this initiation's result.
func (p *S3) confirmEncryptedMultipartResult(ctx context.Context, bucket string, r MultipartCompleteRequest, result UploadResult) error {
	if r.Encryption == nil {
		return nil
	}
	if err := multipartBeforeRequest(ctx, r); err != nil {
		return err
	}
	out, err := p.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(r.Key), VersionId: stringPtrOrNil(result.ProviderVersionID)}, func(o *s3.Options) { o.RetryMaxAttempts = 1 })
	if err != nil {
		return normalizeVersionHistoryError(err)
	}
	if out == nil || out.ContentLength == nil || *out.ContentLength != r.SizeBytes || aws.ToBool(out.DeleteMarker) || aws.ToString(out.ETag) != result.ETag || out.Metadata[ReservedMultipartSessionMetadataKey] != r.SessionID || !validCopySnapshotVersion(out.ResultMetadata, aws.ToString(out.VersionId), result.ProviderVersionID) || !validTrackedProofHeaders(out.ResultMetadata, ReservedMultipartSessionMetadataKey) || !validEncryptionResponse(out.ResultMetadata, r.Encryption) || !validStoredEncryptionResponse(out.Metadata, out.ResultMetadata, r.Encryption) {
		return ErrUnavailable
	}
	return nil
}

func applyPutEncryption(in *s3.PutObjectInput, e *ResolvedObjectEncryption) {
	if e == nil {
		return
	}
	in.ServerSideEncryption = types.ServerSideEncryption(e.Selection.Algorithm)
	in.SSEKMSKeyId = stringPtrOrNil(e.ProviderKeyID)
	in.SSEKMSEncryptionContext = stringPtrOrNil(e.Selection.Context)
	in.BucketKeyEnabled = cloneObjectEncryption(e.Selection).BucketKeyEnabled
	in.Metadata = encryptionProofMetadata(in.Metadata, e)
}

func applyCopyEncryption(in *s3.CopyObjectInput, e *ResolvedObjectEncryption) {
	if e == nil {
		return
	}
	in.ServerSideEncryption = types.ServerSideEncryption(e.Selection.Algorithm)
	in.SSEKMSKeyId = stringPtrOrNil(e.ProviderKeyID)
	in.SSEKMSEncryptionContext = stringPtrOrNil(e.Selection.Context)
	in.BucketKeyEnabled = cloneObjectEncryption(e.Selection).BucketKeyEnabled
	in.Metadata = encryptionProofMetadata(in.Metadata, e)
}

func applyMultipartEncryption(in *s3.CreateMultipartUploadInput, e *ResolvedObjectEncryption) {
	if e == nil {
		return
	}
	in.ServerSideEncryption = types.ServerSideEncryption(e.Selection.Algorithm)
	in.SSEKMSKeyId = stringPtrOrNil(e.ProviderKeyID)
	in.SSEKMSEncryptionContext = stringPtrOrNil(e.Selection.Context)
	in.BucketKeyEnabled = cloneObjectEncryption(e.Selection).BucketKeyEnabled
	in.Metadata = encryptionProofMetadata(in.Metadata, e)
}

func validEncryptionResponse(metadata middleware.Metadata, e *ResolvedObjectEncryption) bool {
	if e == nil {
		return true
	}
	response, ok := awsmiddleware.GetRawResponse(metadata).(*smithyhttp.Response)
	return ok && response != nil && response.Response != nil && validEncryptionHeaders(response.Header, e)
}

// Keep ambiguity out of completion proof. A missing/duplicate or mismatched
// acknowledgment is uncertain after dispatch, never proof of write rejection.
func validEncryptionHeaders(headers http.Header, e *ResolvedObjectEncryption) bool {
	if e == nil {
		return true
	}
	for name := range headers {
		if strings.HasPrefix(strings.ToLower(name), "x-amz-server-side-encryption-customer-") {
			return false
		}
	}
	if !oneEncryptionHeader(headers, "X-Amz-Server-Side-Encryption", e.Selection.Algorithm) {
		return false
	}
	if e.ProviderKeyID == "" {
		return len(encryptionHeaderValues(headers, "X-Amz-Server-Side-Encryption-Aws-Kms-Key-Id")) == 0 && len(encryptionHeaderValues(headers, "X-Amz-Server-Side-Encryption-Bucket-Key-Enabled")) == 0
	}
	if !oneEncryptionHeader(headers, "X-Amz-Server-Side-Encryption-Aws-Kms-Key-Id", e.ProviderKeyID) {
		return false
	}
	bucket := encryptionHeaderValues(headers, "X-Amz-Server-Side-Encryption-Bucket-Key-Enabled")
	if e.Selection.Algorithm == "aws:kms:dsse" {
		return len(bucket) == 0
	}
	if e.Selection.BucketKeyEnabled != nil {
		if *e.Selection.BucketKeyEnabled {
			return len(bucket) == 1 && bucket[0] == "true"
		}
		return len(bucket) == 0 || len(bucket) == 1 && bucket[0] == "false"
	}
	return len(bucket) == 0 || len(bucket) == 1 && (bucket[0] == "false" || bucket[0] == "true")
}

// VerifyEncryptionAcknowledgment checks the private native response before a
// brokered write can settle. A mismatch retains the dispatched recovery intent.
func VerifyEncryptionAcknowledgment(headers http.Header, e ResolvedObjectEncryption) (api.ObjectEncryption, error) {
	if e.Empty() {
		return api.ObjectEncryption{}, nil
	}
	if !e.ValidFor(e.AccountID) || !validEncryptionHeaders(headers, &e) {
		return api.ObjectEncryption{}, ErrUnavailable
	}
	return cloneObjectEncryption(e.Selection), nil
}

func oneEncryptionHeader(headers http.Header, name, value string) bool {
	values := encryptionHeaderValues(headers, name)
	return len(values) == 1 && values[0] == value
}

func validEncryptedSignedPut(out *v4.PresignedHTTPRequest, e *ResolvedObjectEncryption) bool {
	if out == nil || !validEncryptionHeaders(out.SignedHeader, e) || !oneEncryptionHeader(out.SignedHeader, "X-Amz-Meta-"+ReservedObjectEncryptionMetadataKey, e.Proof()) {
		return false
	}
	u, err := url.Parse(out.URL)
	if err != nil {
		return false
	}
	signed := ";" + u.Query().Get("X-Amz-SignedHeaders") + ";"
	for _, name := range []string{"x-amz-server-side-encryption", "x-amz-meta-" + ReservedObjectEncryptionMetadataKey} {
		if !strings.Contains(signed, ";"+name+";") {
			return false
		}
	}
	for _, name := range []string{"x-amz-server-side-encryption-aws-kms-key-id", "x-amz-server-side-encryption-context", "x-amz-server-side-encryption-bucket-key-enabled"} {
		if len(out.SignedHeader.Values(name)) != 0 && !strings.Contains(signed, ";"+name+";") {
			return false
		}
	}
	return e.Selection.Context == "" || oneEncryptionHeader(out.SignedHeader, "X-Amz-Server-Side-Encryption-Context", e.Selection.Context)
}
