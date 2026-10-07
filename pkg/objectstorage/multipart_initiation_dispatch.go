package objectstorage

import "context"

// MultipartInitiationProvider creates a new native upload only after invoking
// the durable dispatch claim. It never adopts an upload from a listing and must
// disable native retries of the non-idempotent creation request.
type MultipartInitiationProvider interface {
	InitiateMultipartUpload(context.Context, string, MultipartCreateRequest, ResolvedObjectEncryption, func(context.Context) error) (string, error)
}

func InitiateMultipartUpload(ctx context.Context, p Provider, bucket string, r MultipartCreateRequest, e ResolvedObjectEncryption, dispatch func(context.Context) error) (string, error) {
	if dispatch == nil {
		return "", ErrInvalid
	}
	provider, ok := p.(MultipartInitiationProvider)
	if !ok {
		return "", ErrUnsupported
	}
	return provider.InitiateMultipartUpload(ctx, bucket, r, e, dispatch)
}

func (p *S3) InitiateMultipartUpload(ctx context.Context, bucket string, r MultipartCreateRequest, e ResolvedObjectEncryption, dispatch func(context.Context) error) (string, error) {
	if !validMultipartCreateRequest(r) || dispatch == nil {
		return "", ErrInvalid
	}
	var encryption *ResolvedObjectEncryption
	if !e.Empty() {
		if !e.ValidFor(e.AccountID) {
			return "", ErrConfiguration
		}
		encryption = &e
	}
	return p.createMultipartEncrypted(ctx, bucket, r, encryption, dispatch)
}
