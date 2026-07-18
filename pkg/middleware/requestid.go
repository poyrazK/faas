// Package middleware holds the cross-component HTTP middleware used by
// every daemon's public listener (spec §11 — Single public listener).
//
// Slice 2 ships three middlewares that close §11 gaps apid needs:
// RequestID, Recovery, and AuthLimit. Gatewayd has had its own
// request-id primitive since M0 — the pkg/middleware copy is the same
// algorithm so the wire header (x-faas-request-id) stays compatible.
package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
)

// RequestIDKey is the context key for the per-request id. Exported so
// downstream packages can read it back out (handlers, slog fields,
// outbound RPCs). Empty string is a no-op set.
type RequestIDKey struct{}

// WithRequestID stores id on ctx and returns the new context. Empty id
// is a no-op so callers can pass through whatever they got.
func WithRequestID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, RequestIDKey{}, id)
}

// RequestIDFrom extracts the request id from r's context, falling back
// to the x-faas-request-id response header set upstream, and finally
// to a fresh uuid hex if neither is present. nil-safe.
func RequestIDFrom(r *http.Request) string {
	if r == nil {
		return NewRequestID()
	}
	if v, ok := r.Context().Value(RequestIDKey{}).(string); ok && v != "" {
		return v
	}
	if v := r.Header.Get("x-faas-request-id"); v != "" {
		return v
	}
	return NewRequestID()
}

// NewRequestID returns a 128-bit random hex (uuid-like without dashes).
// crypto/rand so we don't pull google/uuid just for this. On the
// (extremely unlikely) rand.Read failure we emit zero rather than
// panicking in the request hot path.
func NewRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "00000000000000000000000000000000"
	}
	return hex.EncodeToString(b[:])
}

// RequestID is a middleware that ensures every request has an
// x-faas-request-id header (inbound override OR freshly generated),
// stores it on the context via WithRequestID, and echoes it back on
// the response. Wire-compatible with pkg/gateway/observability so the
// same value flows from gatewayd through the reverse-proxy to apid.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rid := r.Header.Get("x-faas-request-id")
		if rid == "" {
			rid = NewRequestID()
		}
		w.Header().Set("x-faas-request-id", rid)
		r = r.WithContext(WithRequestID(r.Context(), rid))
		next.ServeHTTP(w, r)
	})
}
