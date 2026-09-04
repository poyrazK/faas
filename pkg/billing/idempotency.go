package billing

import "context"

// idempotencyKeyContextKey carries an operation key from an API handler to a
// provider implementation. Providers use the key for their native retry
// protection; keeping the context key in the shared package avoids making the
// apid package depend on provider-private context types.
type idempotencyKeyContextKey struct{}

// ContextWithIdempotencyKey returns a child context carrying key. Empty keys
// are intentionally still stored as empty values so callers can use the
// read-side boolean to distinguish "not supplied" from a provider fallback.
func ContextWithIdempotencyKey(ctx context.Context, key string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, idempotencyKeyContextKey{}, key)
}

// IdempotencyKeyFromContext retrieves a key installed by
// ContextWithIdempotencyKey.
func IdempotencyKeyFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	key, ok := ctx.Value(idempotencyKeyContextKey{}).(string)
	return key, ok && key != ""
}
