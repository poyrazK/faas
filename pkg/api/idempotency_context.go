package api

import (
	"context"
	"strings"
)

// idempotencyKeyContextKey is deliberately private so callers cannot collide
// with the SDK's request metadata by using a plain context value.
type idempotencyKeyContextKey struct{}

// ContextWithIdempotencyKey returns a child context whose key is used for the
// next mutating SDK request. The value is request-scoped metadata. Use a
// distinct key for each logical operation on the same route; realtime channel
// publish keys are bound to the decoded message payload and binary flag.
//
// An empty key clears an inherited value. This is useful when a caller shares
// a parent context between a deploy mutation and unrelated setup calls.
func ContextWithIdempotencyKey(ctx context.Context, key string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, idempotencyKeyContextKey{}, strings.TrimSpace(key))
}

// IdempotencyKeyFromContext returns the request key attached by
// ContextWithIdempotencyKey, or an empty string when the context has no key.
func IdempotencyKeyFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	key, _ := ctx.Value(idempotencyKeyContextKey{}).(string)
	return strings.TrimSpace(key)
}
