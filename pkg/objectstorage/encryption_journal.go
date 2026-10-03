package objectstorage

import "context"

type encryptionRequestRecorderKey struct{}
type encryptionWriteRecorderKey struct{}

// WithEncryptionWriteRecorder fences the native write after validation and key
// checking, so an unavailable key never dispatches or bills an object mutation.
func WithEncryptionWriteRecorder(ctx context.Context, before func(context.Context) error) context.Context {
	return context.WithValue(ctx, encryptionWriteRecorderKey{}, before)
}

func beforeEncryptionWrite(ctx context.Context) error {
	if before, ok := ctx.Value(encryptionWriteRecorderKey{}).(func(context.Context) error); ok && before != nil {
		return before(ctx)
	}
	return ctx.Err()
}

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
