// Per-request observability helpers (request ID, start timestamp). Kept in
// their own file so the handler stays close to its request-path concerns.
package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"time"
)

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
