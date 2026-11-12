package faas

import (
	"context"
	"net/http"
)

// IdempotencyKey is the typed string callers pass to WithIdempotencyKey
// to pin a stable key for retry-safe mutating calls. Any non-empty
// string is accepted; the server's replay middleware (apid/server.go
// ::idempotent) caches the response for 24h keyed on this value.
//
// IdempotencyKey has no validation in the SDK; the server validates
// the format. Callers that want a UUIDv4 per attempt should use
// google/uuid (or crypto/rand) and pass the result here.
type IdempotencyKey string

type idempotencyKeyCtxKey struct{}

// WithIdempotencyKey returns a context.Context that carries the key
// for the next HTTP request made through this Client. The key is
// extracted by the idempotency round-tripper (installed by NewClient)
// and injected as the Idempotency-Key request header before the
// auto-mint in internal/api/client.do fires.
//
// Pass the returned context to any mutating Client method:
//
//	ctx = faas.WithIdempotencyKey(ctx, "deploy-attempt-3")
//	dep, err := c.Deploy(ctx, slug, req)
//
// If the caller does not supply a key, the SDK auto-mints a UUIDv4
// per call. Both paths are safe; the difference is that supplying
// a stable key makes retries deterministic.
func WithIdempotencyKey(ctx context.Context, key IdempotencyKey) context.Context {
	if key == "" {
		return ctx
	}
	return context.WithValue(ctx, idempotencyKeyCtxKey{}, key)
}

// WithoutIdempotencyKey preserves cancellation and deadlines while suppressing
// an inherited mutation key for an independent read.
func WithoutIdempotencyKey(ctx context.Context) context.Context {
	return context.WithValue(ctx, idempotencyKeyCtxKey{}, IdempotencyKey(""))
}

// idempotencyKeyFromContext extracts the key. Returns "" if absent.
func idempotencyKeyFromContext(ctx context.Context) IdempotencyKey {
	v, _ := ctx.Value(idempotencyKeyCtxKey{}).(IdempotencyKey)
	return v
}

// idempotencyRoundTripper injects the Idempotency-Key header from the
// request context before delegating to the wrapped transport. When
// the context carries no key, the shim is a no-op and the auto-mint
// in internal/api/client.do takes over.
type idempotencyRoundTripper struct {
	inner http.RoundTripper
}

func newIdempotencyRoundTripper(inner http.RoundTripper) *idempotencyRoundTripper {
	if inner == nil {
		inner = http.DefaultTransport
	}
	return &idempotencyRoundTripper{inner: inner}
}

func (r *idempotencyRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if key := idempotencyKeyFromContext(req.Context()); key != "" {
		// An explicit caller-supplied key from WithIdempotencyKey
		// always wins — even over the auto-mint that
		// internal/api/client.do may have already set. This matches
		// the documented "stable key for retries" contract: if the
		// caller pinned a key, the auto-mint is a no-op.
		//
		// Clone the header so we don't mutate the caller's request
		// (http.Request.Header is shared via the Transport contract).
		req2 := req.Clone(req.Context())
		req2.Header = req.Header.Clone()
		req2.Header.Set("Idempotency-Key", string(key))
		req = req2
	}
	return r.inner.RoundTrip(req)
}
