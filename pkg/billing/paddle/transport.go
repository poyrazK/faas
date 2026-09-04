package paddle

import (
	"net/http"
	"strings"
)

// IdempotencyKeyHeader is the canonical HTTP header Paddle uses to
// collapse a duplicate POST into a single transaction on the
// merchant dashboard. The SDK v5.2.0 does not expose this header as
// a request option, so we inject it from a custom http.RoundTripper
// that wraps the SDK's transport. If/when the SDK adds native
// Idempotency-Key support, the RoundTripper becomes a no-op (the
// SDK sets the header itself) and this constant can be retired.
//
// Today (v5.2.0): the SDK reads paddle.ContextWithTransitID(ctx,
// idem) and stamps it as both X-Transit-Id (current) and the
// deprecated X-Transaction-Id header — see
// github.com/PaddleHQ/paddle-go-sdk/v5/internal/client/client.go:98-101.
// Our RoundTripper reads X-Transit-Id and copies it as
// Idempotency-Key. Single source of truth: the caller sets the
// transit ID via paddle.ContextWithTransitID; both the SDK's
// deprecated header and our durable header are stamped from the
// same context value, no call-site drift.
//
// Paddle's API server may not honor Idempotency-Key today
// (the SDK team is working on native support — see ADR-032 §4).
// The header presence is observable on the wire for ops debugging
// even if the server collapses on a different key today; the
// forward-compat is that once the SDK ships native Idempotency-Key
// support, the SDK's own header value matches what we inject
// (the same `faas-overage-<acctID>-<YYYY-MM>` shape) because we
// reuse the SDK's ContextWithTransitID plumbing.
const IdempotencyKeyHeader = "Idempotency-Key"

// TransitIDHeader is the SDK's current (non-deprecated) header for
// per-request idempotency tokens. Read by our RoundTripper as the
// source value for the Idempotency-Key header. Duplicated here as a
// constant so the RoundTripper is self-documenting; the SDK's own
// client.go:99 sets this header from the same context value.
const TransitIDHeader = "X-Transit-Id"

// idempotencyRoundTripper wraps an inner http.RoundTripper to inject
// the Idempotency-Key header on Paddle write requests. See the
// package comment for the full design rationale.
//
// The wrapper is constructed by NewIdempotencyRT; do not
// instantiate it directly so the inner transport is always set
// (nil would crash with a nil-pointer deref on RoundTrip).
type idempotencyRoundTripper struct {
	inner http.RoundTripper
}

// NewIdempotencyRT wraps an http.RoundTripper so every Paddle SDK
// request flows through the Idempotency-Key injector. The SDK
// exposes paddle.WithClient(c client.HTTPDoer) for this purpose; we
// use it in the provider constructor (provider.go:NewProvider) so
// every Paddle SDK call — CreateTransaction, GetProduct, etc. — flows
// through this wrapper.
//
// Usage:
//
//	rt := NewIdempotencyRT(http.DefaultTransport)
//	client := paddle.New(apiKey, paddle.WithClient(&http.Client{Transport: rt}))
//
// Returns the wrapped http.RoundTripper; the caller composes it
// into a *http.Client via &http.Client{Transport: rt}. A nil inner
// transport falls back to http.DefaultTransport so the constructor
// never returns a non-functional wrapper.
func NewIdempotencyRT(inner http.RoundTripper) http.RoundTripper {
	if inner == nil {
		inner = http.DefaultTransport
	}
	return &idempotencyRoundTripper{inner: inner}
}

// RoundTrip implements http.RoundTripper. On POSTs to idempotent Paddle
// write namespaces, copies the SDK's X-Transit-Id header (set by the SDK
// from paddle.ContextWithTransitID) as Idempotency-Key. All other requests
// are passed through unmodified.
//
// Per the net/http RoundTripper contract, an implementation MUST
// NOT mutate the caller's *http.Request — the request may be reused
// by the caller (the Paddle SDK does not, today, but tests and
// retry middleware do). We clone the request + its Header before
// mutating, so the inbound request's header set is unchanged after
// RoundTrip returns. The SDK's own internal/client/client.go:95-101
// mutates req.Header without cloning — that is technically a SDK-
// side contract violation that we cannot fix from this package; we
// copy the SDK's value into our injected header on a cloned
// request so the caller's view is unobserved.
func (rt *idempotencyRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if shouldInjectIdempotencyKey(req) {
		if transitID := req.Header.Get(TransitIDHeader); transitID != "" {
			cloned := req.Clone(req.Context())
			cloned.Header = req.Header.Clone()
			cloned.Header.Set(IdempotencyKeyHeader, transitID)
			return rt.inner.RoundTrip(cloned)
		}
	}
	return rt.inner.RoundTrip(req)
}

// transactionsPathSegment is the path segment Paddle's API uses for
// the transaction-write endpoints. The RoundTripper only injects
// Idempotency-Key on POSTs whose URL contains this segment so
// non-transaction calls (GET /transactions/{id} for reads,
// /products, /customers, etc.) pass through unmodified.
//
// Segment matching (rather than HasSuffix) covers nested
// transaction endpoints like /transactions/{id}/revise that
// the Paddle API exposes today; the segment-equality check
// future-proofs the wrapper against future additions in the
// /transactions namespace.
const transactionsPathSegment = "transactions"

// shouldInjectIdempotencyKey gates the header injection to POSTs
// targeting transaction, subscription, or adjustment APIs. Idempotency-Key on a GET is a
// documented anti-pattern (GETs must be safe and idempotent at the
// protocol level); other writes (product create, customer update) are not
// currently idempotency-keyed because
// the retry budget is on the meterd side, not the merchant side.
//
// Path matching uses segment equality rather than HasSuffix so
// nested transaction endpoints (/transactions/{id}/revise and
// friends) receive the header while non-transaction paths
// (/products, /customers, /transactions-foo unrelated name) do not.
// The exact list of nested transaction endpoints evolves with the
// Paddle API; the segment-equality check covers them generically.
func shouldInjectIdempotencyKey(req *http.Request) bool {
	if req.Method != http.MethodPost {
		return false
	}
	if req.URL == nil || req.URL.Path == "" {
		return false
	}
	for _, seg := range strings.Split(strings.TrimPrefix(req.URL.Path, "/"), "/") {
		if seg == transactionsPathSegment || seg == "subscriptions" || seg == "adjustments" {
			return true
		}
	}
	return false
}
