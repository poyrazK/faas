// Per-request observability helpers (request ID, start timestamp). Kept in
// their own file so the handler stays close to its request-path concerns.
package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// RequestIDMiddleware stamps a request id before a public edge handler runs.
// gatewayd-internal also stamps the id at its routing boundary, but the
// public listener can time out before that hop returns. Stamping here keeps
// the correlation id available on those early 504 responses and forwards it
// through the internal proxy.
func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rid := r.Header.Get(api.RequestIDHeader)
		if rid == "" {
			rid = newRequestID()
		}
		w.Header().Set(api.RequestIDHeader, rid)
		r = r.WithContext(WithRequestID(r.Context(), rid)) //nolint:contextcheck // request ctx is the canonical inbound ctx at the HTTP handler boundary.
		next.ServeHTTP(w, r)
	})
}

// requestIDKey is the context key used to thread a per-request id through
// downstream handlers and outbound RPCs.
type requestIDKey struct{}

// WithRequestID stores id on ctx and returns the new context. Empty id is a
// no-op (so callers can pass through whatever they got).
func WithRequestID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, requestIDKey{}, id)
}

// sidecarPortKey (issue #463 / ADR-069 / ADR-071 / PR-C §5)
// is the context key used to thread a per-request sidecar
// port override through the gateway handler to the
// forwarder. The handler stamps the override after
// SplitHostSelector / SidecarSelectorForApp; the forwarder
// reads it (via SidecarPortFrom) and prefers it over
// Target.Port when set. 0 = no override, meaning the
// forwarder falls back to Target.Port (the main workload's
// port) — same value as the request not mentioning a
// sidecar at all.
type sidecarPortKey struct{}

// WithSidecarPort stamps port on r's context. port=0 is a
// no-op so callers can short-circuit the unset case.
func WithSidecarPort(ctx context.Context, port int) context.Context {
	if port == 0 {
		return ctx
	}
	return context.WithValue(ctx, sidecarPortKey{}, port)
}

// withSidecarPort is the *http.Request convenience wrapper
// for WithSidecarPort — matches the WithRequestID pattern
// in this file's surrounding code.
func withSidecarPort(r *http.Request, port int) *http.Request {
	if r == nil || port == 0 {
		return r
	}
	return r.WithContext(WithSidecarPort(r.Context(), port))
}

// SidecarPortFrom returns the sidecar port override on r's
// context, or 0 if none. The forwarder reads this on every
// request — the lookup is O(1) (Go's context.Value walks
// a small slice of typed keys, no map allocation).
func SidecarPortFrom(r *http.Request) int {
	if r == nil {
		return 0
	}
	if v, ok := r.Context().Value(sidecarPortKey{}).(int); ok {
		return v
	}
	return 0
}

// requestIDFrom returns the request id from the request's context, falling
// back to the x-faas-request-id response header the response side set, and
// finally to a fresh uuid hex if neither is present.
func requestIDFrom(r *http.Request) string {
	if r == nil {
		return newRequestID()
	}
	if v, ok := r.Context().Value(requestIDKey{}).(string); ok && v != "" {
		return v
	}
	if v := r.Header.Get("x-faas-request-id"); v != "" {
		return v
	}
	return newRequestID()
}

// newRequestID returns a 128-bit random hex (uuid-like without the dashes).
// crypto/rand so we don't pull google/uuid as a dep just for this.
func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Extremely unlikely (rand.Read failure is fatal-grade); emit zero
		// rather than panicking in the request hot path.
		return "00000000000000000000000000000000"
	}
	return hex.EncodeToString(b[:])
}

// startTimeKey is the context key for the request-received timestamp.
type startTimeKey struct{}

// WithStartTime stores t on ctx so downstream goroutines can measure elapsed
// time from the same instant.
func WithStartTime(ctx context.Context, t time.Time) context.Context {
	return context.WithValue(ctx, startTimeKey{}, t)
}

// startTime extracts the request-received timestamp from r's context if the
// upstream middleware set one, otherwise falls back to r's arrival time.
func startTime(r *http.Request) time.Time {
	if r == nil {
		return time.Now()
	}
	if t, ok := r.Context().Value(startTimeKey{}).(time.Time); ok {
		return t
	}
	return time.Now()
}

// routeLabelKey (ADR-093) is the context key used to thread the
// per-request route label from the post-Lookup derivation site
// (handler.go's `haveApp:` label) to Handler.observe's single exit
// funnel. The label is the result of routeLabelSet.admit() —
// i.e. method + raw path bounded by the per-app cap, with
// __route_other__ as the overflow bucket. Empty string = the
// reserved "no appID" sentinel (the same value the
// gateway_requests_total{app="-"} series uses).
//
// The stash is module-private-but-package-internal: only the
// Handler observes the request lifecycle, so the key is not
// exported. The lookup is O(1) (Go's context.Value walks a small
// slice of typed keys, no map allocation) so the hot path stays
// allocation-free.
type routeLabelKey struct{}

// WithRouteLabel stores label on ctx. label="" is a no-op so
// callers can short-circuit the "feature off" case without a
// string allocation.
func WithRouteLabel(ctx context.Context, label string) context.Context {
	if label == "" {
		return ctx
	}
	return context.WithValue(ctx, routeLabelKey{}, label)
}

// withRouteLabel is the *http.Request convenience wrapper. Mirrors
// the withSidecarPort pattern above.
func withRouteLabel(r *http.Request, label string) *http.Request {
	if r == nil || label == "" {
		return r
	}
	return r.WithContext(WithRouteLabel(r.Context(), label))
}

// RouteLabelFrom returns the per-request route label from r's
// context, or "" if none. Handler.observe reads this on every
// HTTP exit path; the lookup is O(1) and the empty-string
// fallback is the same sentinel the gateway_requests_total{app="-"}
// reader uses.
func RouteLabelFrom(r *http.Request) string {
	if r == nil {
		return ""
	}
	if v, ok := r.Context().Value(routeLabelKey{}).(string); ok {
		return v
	}
	return ""
}
