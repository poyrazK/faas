package objectstorage

import "context"

type encryptionRequestRecorderKey struct{}

// WithEncryptionRequestRecorder accounts for actual native key requests. The
// callback runs after validation and before each KMS dispatch, including when
// permission checking fails. It cannot change the captured key selection.
func WithEncryptionRequestRecorder(ctx context.Context, before func(context.Context) error) context.Context {
	return context.WithValue(ctx, encryptionRequestRecorderKey{}, before)
}

func beforeEncryptionKeyRequest(ctx context.Context) error {
	if before, ok := ctx.Value(encryptionRequestRecorderKey{}).(func(context.Context) error); ok && before != nil {
		return before(ctx)
	}
	return ctx.Err()
}

// EnsureMultipartWithEncryption consumes only a persisted private snapshot.
// Encryption must never fall back to an ordinary provider initialization.
func EnsureMultipartWithEncryption(ctx context.Context, p Provider, bucket string, r MultipartCreateRequest, e ResolvedObjectEncryption) (string, error) {
	if e.Empty() {
		return p.EnsureMultipartUpload(ctx, bucket, r)
	}
	provider, ok := p.(ObjectEncryptionProvider)
	if !ok || !e.ValidFor(e.AccountID) {
		return "", ErrConfiguration
	}
	return provider.EnsureEncryptedMultipart(ctx, bucket, r, e)
}
