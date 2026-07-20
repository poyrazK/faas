// First-upstream-byte observation for the wake-latency SLO (spec §6.3,
// §12 `gateway_wake_latency_seconds`). The histogram is supposed to measure
// "request-received to first upstream byte", but the handler can only see the
// reverse-proxy call returning (which is when the upstream response body has
// been fully copied). The stdlib offers the boundary we need via
// `httptrace.ClientTrace.GotFirstResponseByte`; this file wires it into the
// proxy's RoundTripper so the timestamp lands on the inbound request's
// context before the handler resumes.
//
// The handler reads it back with FirstByteFrom after proxyFor.ServeHTTP
// returns and observes firstByteAt.Sub(wakeStart) — the actual wake latency
// we promised the dashboard.
//
// Concurrency: the trace's callback fires on the goroutine that called
// RoundTrip (same goroutine as the reverse proxy), so no synchronisation is
// needed between the writer and the reader of the context value.
package gateway

import (
	"context"
	"net/http"
	"net/http/httptrace"
	"sync"
	"time"
)

// firstByteAtKey is the context key for the per-request firstByteRecorder.
// The recorder is a pointer (not a value) so the trace callback can mutate
// the timestamp it owns and the handler reads it back without a race.
type firstByteAtKey struct{}

// firstByteRecorder is a 1-slot mutable cell the trace stamps and the
// handler reads. The trace callback fires on the RoundTripper's goroutine,
// which is the same goroutine as the handler's ReverseProxy.ServeHTTP call,
// so a plain time.Time field is safe without synchronisation.
//
// We use a pointer rather than `time.Time` directly on the context so we
// can distinguish "not stamped yet" (nil) from "stamped at zero" (a real
// observation at the epoch). The handler treats the bool as the truth.
type firstByteRecorder struct {
	mu  sync.Mutex
	at  time.Time
	set bool
}

// record stamps the timestamp. Called by the trace's GotFirstResponseByte.
func (r *firstByteRecorder) record(t time.Time) {
	r.mu.Lock()
	r.at = t
	r.set = true
	r.mu.Unlock()
}

// read returns the stamped timestamp if any. Safe to call concurrently with
// record; the mutex is the cheap insurance in case some future caller
// invokes us from a different goroutine than the trace.
func (r *firstByteRecorder) read() (time.Time, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.at, r.set
}

// WithFirstByteRecorder installs a recorder on ctx so the wake-timing
// RoundTripper can stamp it. Tests use this directly; production wires it
// from the handler.
func WithFirstByteRecorder(ctx context.Context, rec *firstByteRecorder) context.Context {
	if rec == nil {
		return ctx
	}
	return context.WithValue(ctx, firstByteAtKey{}, rec)
}

// FirstByteFrom returns the timestamp the wake-timing RoundTripper captured
// at "first upstream response byte", if any. The bool is false when the
// reverse proxy never reached the first-byte boundary (e.g. upstream
// connection failed before headers arrived) — the handler should fall back
// to a full-duration observation and log a warning in that case.
func FirstByteFrom(r *http.Request) (time.Time, bool) {
	if r == nil {
		return time.Time{}, false
	}
	rec, ok := r.Context().Value(firstByteAtKey{}).(*firstByteRecorder)
	if !ok || rec == nil {
		return time.Time{}, false
	}
	return rec.read()
}

// firstByteRoundTripper is an http.RoundTripper wrapper that stamps the
// inbound request's recorder at the moment the first upstream response byte
// arrives. It wraps an inner RoundTripper (typically *http.Transport) so
// existing connection-pool behaviour is preserved.
//
// The trace uses httptrace.WithClientTrace rather than a custom writer because
// it is the only stdlib-blessed signal for "first response byte", and because
// the reverse proxy composes well with it (its own trace is attached
// separately and both fire).
type firstByteRoundTripper struct {
	inner http.RoundTripper
}

// newFirstByteRoundTripper wraps inner with the first-byte stampler.
func newFirstByteRoundTripper(inner http.RoundTripper) *firstByteRoundTripper {
	return &firstByteRoundTripper{inner: inner}
}

// RoundTrip attaches the trace to the outbound request, then forwards to the
// inner transport. The trace looks up the recorder on the outbound request's
// context (which is what the handler passed via WithFirstByteRecorder); the
// reverse proxy preserves it through the clone.
func (rt *firstByteRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	rec, _ := req.Context().Value(firstByteAtKey{}).(*firstByteRecorder)
	trace := &httptrace.ClientTrace{
		GotFirstResponseByte: func() {
			if rec != nil {
				rec.record(time.Now())
			}
		},
	}
	req2 := req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
	return rt.inner.RoundTrip(req2)
}