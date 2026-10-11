package objectstorage

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ ObjectEncryptionProvider = (*GCS)(nil)

// AES256 uses Google's managed baseline. CMEK, encryption contexts, bucket
// keys and dual-layer KMS require distinct native contracts and fail closed.
func (p *GCS) CheckEncryptionKey(ctx context.Context, e ResolvedObjectEncryption) error {
	if e.Empty() {
		return ErrInvalid
	}
	if err := p.encryption.VerifySnapshot(e.AccountID, e); err != nil {
		return err
	}
	if e.Selection.Algorithm != "AES256" {
		return ErrUnsupported
	}
	return ctx.Err()
}

func ProviderReadEncryption(p Provider, c EncryptionConfig, account string, headers http.Header) (api.ObjectEncryption, error) {
	if _, ok := p.(*GCS); !ok {
		return c.PublicReadEncryption(account, headers)
	}
	for name := range headers {
		n := strings.ToLower(name)
		if strings.HasPrefix(n, "x-goog-encryption-") || strings.HasPrefix(n, "x-amz-server-side-encryption") {
			return api.ObjectEncryption{}, ErrUnavailable
		}
	}
	return api.ObjectEncryption{Algorithm: "AES256"}, nil
}

func (p *GCS) WriteEncryptedObject(ctx context.Context, bucket, key, receipt string, body io.Reader, size int64, m ObjectMetadata, e ResolvedObjectEncryption) (UploadResult, error) {
	if !validGCSReceipt(key, receipt, size) || size > 0 && body == nil || ValidateObjectMetadata(m) != nil {
		return UploadResult{}, errors.Join(ErrWriteRejected, ErrInvalid)
	}
	if err := p.CheckEncryptionKey(ctx, e); err != nil {
		return UploadResult{}, errors.Join(ErrWriteRejected, err)
	}
	headers := gcsUploadHeaders(m)
	headers.Set("x-goog-meta-"+ReservedUploadReceiptMetadataKey, receipt)
	headers.Set("x-goog-meta-"+ReservedObjectEncryptionMetadataKey, e.Proof())
	if err := beforeEncryptionWrite(ctx); err != nil {
		return UploadResult{}, errors.Join(ErrWriteRejected, err)
	}
	response, err := p.gcsStreamRequest(ctx, http.MethodPut, bucket, key, nil, headers, body, size)
	if err != nil {
		return UploadResult{}, err
	}
	_ = response.Body.Close()
	ack, err := p.verifyWriteAcknowledgment(response.Header)
	if err != nil {
		return UploadResult{}, err
	}
	if err = beforeEncryptionKeyRequest(ctx); err != nil {
		return UploadResult{}, err
	}
	proof, err := p.ConfirmEncryptedObject(ctx, bucket, key, receipt, size, e)
	if err != nil || proof.ProviderVersionID != ack.ProviderVersionID {
		return UploadResult{}, ErrUnavailable
	}
	proof.ETag = ack.ETag
	return proof, nil
}

func (p *GCS) PresignEncryptedPut(ctx context.Context, bucket string, r SignRequest, c ObjectWriteConditions, receipt string, e ResolvedObjectEncryption) (SignedRequest, error) {
	if err := p.CheckEncryptionKey(ctx, e); err != nil {
		return SignedRequest{}, err
	}
	if r.SizeBytes == nil || r.Method != http.MethodPut || !validGCSReceipt(r.Key, receipt, *r.SizeBytes) || !c.Valid() {
		return SignedRequest{}, ErrInvalid
	}
	return p.presignGCSConditionalPut(ctx, bucket, r, c, http.Header{"x-goog-meta-" + ReservedUploadReceiptMetadataKey: {receipt}, "x-goog-meta-" + ReservedObjectEncryptionMetadataKey: {e.Proof()}})
}

func (p *GCS) CopyEncryptedObject(ctx context.Context, bucket, receipt string, r CopyObjectRequest, source CopySourceSnapshot, c CopySourceConditions, e ResolvedObjectEncryption) (CopyObjectResult, error) {
	if err := p.CheckEncryptionKey(ctx, e); err != nil {
		return CopyObjectResult{}, errors.Join(ErrWriteRejected, err)
	}
	return p.copyGCSTrackedEncrypted(ctx, bucket, bucket, receipt, r, source, c, &e)
}

func (p *GCS) EnsureEncryptedMultipart(ctx context.Context, bucket string, r MultipartCreateRequest, e ResolvedObjectEncryption) (string, error) {
	if err := p.CheckEncryptionKey(ctx, e); err != nil {
		return "", err
	}
	return p.ensureGCSMultipart(ctx, bucket, r, &e)
}

func (p *GCS) CompleteEncryptedMultipart(ctx context.Context, bucket string, r MultipartCompleteRequest, c ObjectWriteConditions, e ResolvedObjectEncryption) (MultipartCompletionResult, error) {
	if err := p.CheckEncryptionKey(ctx, e); err != nil {
		return MultipartCompletionResult{}, err
	}
	r.Encryption = &e
	return p.CompleteMultipartWithResult(ctx, bucket, r, c)
}

func validGCSStoredEncryption(object gcsObjectState, e ResolvedObjectEncryption) bool {
	return object.KMSKeyName == "" && object.Metadata[ReservedObjectEncryptionMetadataKey] == e.Proof() && e.Selection.Algorithm == "AES256"
}

func (p *GCS) ConfirmEncryptedObject(ctx context.Context, bucket, key, receipt string, size int64, e ResolvedObjectEncryption) (UploadResult, error) {
	if err := p.CheckEncryptionKey(ctx, e); err != nil {
		return UploadResult{}, err
	}
	if !validGCSReceipt(key, receipt, size) {
		return UploadResult{}, ErrInvalid
	}
	object, err := p.gcsProofObject(ctx, bucket, key)
	if err != nil {
		return UploadResult{}, normalizeGCS(err)
	}
	if object.Key != key {
		return UploadResult{}, ErrUnavailable
	}
	proof, err := gcsReceiptProof(object, receipt, size, false)
	if err != nil {
		return proof, err
	}
	if !validGCSStoredEncryption(object, e) {
		return UploadResult{}, ErrConflict
	}
	proof.Encryption = cloneObjectEncryption(e.Selection)
	return proof, nil
}

func (p *GCS) ConfirmEncryptedObjectHistory(ctx context.Context, bucket string, r ObjectHistoryProofRequest, e ResolvedObjectEncryption) (ObjectHistoryProofPage, error) {
	if err := p.CheckEncryptionKey(ctx, e); err != nil {
		return ObjectHistoryProofPage{Cursor: r.Cursor}, err
	}
	r.Encryption = &e
	return p.ConfirmTrackedObjectHistory(ctx, bucket, r)
}

// Native GCS PUT responses do not establish encryption intent. The gateway
// must confirm the stored receipt and generation before publishing success.
func VerifyProviderEncryptionAcknowledgment(p Provider, headers http.Header, e ResolvedObjectEncryption) (api.ObjectEncryption, error) {
	if _, ok := p.(*GCS); ok {
		if e.Empty() {
			return api.ObjectEncryption{}, nil
		}
		if !e.ValidFor(e.AccountID) || e.Selection.Algorithm != "AES256" || len(encryptionHeaderValues(headers, "X-Goog-Encryption-Kms-Key-Name")) != 0 {
			return api.ObjectEncryption{}, ErrUnavailable
		}
		return cloneObjectEncryption(e.Selection), nil
	}
	return VerifyEncryptionAcknowledgment(headers, e)
}
