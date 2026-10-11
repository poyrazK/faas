package objectstorage

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
)

// MultipartInitiationRecoveryProvider can discover an existing native upload
// without creating one. An empty listing is only a negative observation; it
// does not prove that an earlier initiation did not reach the provider.
//
// A returned identity is a candidate, not proof of session ownership: S3 upload
// listings do not expose the session metadata. Callers must establish original
// journal authority and exclusive ownership of the key before adopting it.
type MultipartInitiationRecoveryProvider interface {
	RecoverMultipartUpload(context.Context, string, MultipartCreateRequest) (string, error)
}

// RecoverMultipartUpload never falls back to EnsureMultipartUpload, because
// that operation may create a second upload after an uncertain initiation.
// ErrNotFound must not be used to retire a writer receipt or authorize a retry.
func RecoverMultipartUpload(ctx context.Context, provider Provider, bucket string, r MultipartCreateRequest) (string, error) {
	if !validMultipartCreateRequest(r) {
		return "", ErrInvalid
	}
	recovery, ok := provider.(MultipartInitiationRecoveryProvider)
	if !ok {
		return "", ErrUnsupported
	}
	id, err := recovery.RecoverMultipartUpload(ctx, bucket, r)
	if err != nil {
		return "", err
	}
	if !validMultipartUploadID(id) {
		return "", ErrUnavailable
	}
	return id, nil
}

func validMultipartCreateRequest(r MultipartCreateRequest) bool {
	return r.SessionID != "" && len(r.SessionID) <= 128 && ValidKey(r.Key) && r.SizeBytes >= 0 && r.SizeBytes <= api.MaxObjectUploadBytes && ValidateObjectMetadata(r.Metadata) == nil
}

func (p *S3) RecoverMultipartUpload(ctx context.Context, bucket string, r MultipartCreateRequest) (string, error) {
	if !validMultipartCreateRequest(r) {
		return "", ErrInvalid
	}
	id, err := p.discoverMultipartUpload(ctx, bucket, r)
	if err != nil {
		return "", err
	}
	if id == "" {
		return "", ErrNotFound
	}
	return id, nil
}
