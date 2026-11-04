package faas

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// GregaleRevisionHeader identifies an exact deployment for one app. It is
// deliberately not propagated to downstream services: revision pins are
// scoped to the app that received them.
const GregaleRevisionHeader = "X-Gregale-Revision"

// GregaleReleaseHeader identifies an immutable project deployment graph.
const GregaleReleaseHeader = "X-Gregale-Release"

// GregaleDeadlineHeader carries the time a request may still run, in
// milliseconds. Gregale sets it on every request it forwards to an app; on a
// call to another app's service it tells the service proxy when the caller's
// own request runs out, so the call ends then (ADR-20261102082606).
const GregaleDeadlineHeader = "X-Faas-Deadline-Ms"

// gregaleDeadlineMaxMs bounds an accepted X-Faas-Deadline-Ms: the longest
// request budget of any plan, with room to spare.
const gregaleDeadlineMaxMs = 24 * 60 * 60 * 1000

type gregaleReleaseContextKey struct{}

type gregaleDeadlineContextKey struct{}

// WithGregaleDeadline returns a context carrying when the current Gregale
// request runs out. A zero deadline is ignored.
func WithGregaleDeadline(ctx context.Context, deadline time.Time) context.Context {
	if deadline.IsZero() {
		return ctx
	}
	return context.WithValue(ctx, gregaleDeadlineContextKey{}, deadline)
}

// GregaleDeadlineFromContext returns when the current Gregale request runs
// out, if the middleware captured it. It does not cancel the context: the
// handler decides what to do with the time left.
func GregaleDeadlineFromContext(ctx context.Context) (time.Time, bool) {
	deadline, ok := ctx.Value(gregaleDeadlineContextKey{}).(time.Time)
	return deadline, ok && !deadline.IsZero()
}

// parseGregaleDeadlineMs reads one X-Faas-Deadline-Ms value; 0 when it is
// missing, repeated or not a positive bounded integer.
func parseGregaleDeadlineMs(values []string) int64 {
	if len(values) != 1 {
		return 0
	}
	ms, err := strconv.ParseInt(strings.TrimSpace(values[0]), 10, 64)
	if err != nil || ms <= 0 || ms > gregaleDeadlineMaxMs {
		return 0
	}
	return ms
}

// WithGregaleRelease returns a context carrying the selected project release.
// The value is treated as an opaque ID; an empty or malformed value is ignored.
func WithGregaleRelease(ctx context.Context, release string) context.Context {
	release = cleanGregaleRelease(release)
	if release == "" {
		return ctx
	}
	return context.WithValue(ctx, gregaleReleaseContextKey{}, release)
}

// GregaleReleaseFromContext returns the release captured for the current
// request, if any.
func GregaleReleaseFromContext(ctx context.Context) (string, bool) {
	release, ok := ctx.Value(gregaleReleaseContextKey{}).(string)
	return release, ok && release != ""
}

// GregaleReleaseMiddleware captures the release selected by Gregale and the
// request's deadline (X-Faas-Deadline-Ms) on an inbound HTTP request and makes
// them available to outbound service calls made with that request's context.
//
// The gateway validates the incoming release before forwarding it. This
// middleware preserves that context; it does not grant authority to select a
// deployment on its own.
func GregaleReleaseMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		values := r.Header.Values(GregaleReleaseHeader)
		if len(values) == 1 {
			if release := cleanGregaleRelease(values[0]); release != "" {
				r = r.WithContext(WithGregaleRelease(r.Context(), release))
			}
		}
		if ms := parseGregaleDeadlineMs(r.Header.Values(GregaleDeadlineHeader)); ms > 0 {
			r = r.WithContext(WithGregaleDeadline(r.Context(), time.Now().Add(time.Duration(ms)*time.Millisecond)))
		}
		next.ServeHTTP(w, r)
	})
}

// NewGregaleReleaseTransport returns a RoundTripper that forwards the
// request-scoped release and the time the request has left to managed Gregale
// service hosts (for example, billing.svc.gregale). It never forwards a revision pin to a service because
// that pin belongs to the caller app, not the target app. Requests to other
// hosts are left untouched.
func NewGregaleReleaseTransport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return gregaleReleaseRoundTripper{base: base}
}

type gregaleReleaseRoundTripper struct {
	base http.RoundTripper
}

func (t gregaleReleaseRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL == nil || !isGregaleServiceHost(req.URL.Hostname()) {
		return t.base.RoundTrip(req)
	}

	// Work on a shallow request/header copy to honor RoundTripper's contract
	// not to mutate the caller's request.
	cloned := req.Clone(req.Context())
	cloned.Header = req.Header.Clone()
	cloned.Header.Del(GregaleRevisionHeader)

	if release, ok := GregaleReleaseFromContext(req.Context()); ok &&
		cleanGregaleRelease(cloned.Header.Get(GregaleReleaseHeader)) == "" {
		cloned.Header.Set(GregaleReleaseHeader, release)
	}
	if deadline, ok := GregaleDeadlineFromContext(req.Context()); ok {
		setGregaleDeadline(cloned.Header, time.Until(deadline))
	}
	return t.base.RoundTrip(cloned)
}

func isGregaleServiceHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	return strings.HasSuffix(host, ".svc.gregale")
}

func cleanGregaleRelease(release string) string {
	release = strings.TrimSpace(release)
	if release == "" || strings.ContainsAny(release, ", \t\r\n") {
		return ""
	}
	return release
}

// setGregaleDeadline sets X-Faas-Deadline-Ms to the time left, keeping a
// shorter valid value the caller set itself. A request with no time left
// still sends 1 ms, so the service proxy refuses the call instead of waking
// the target for an answer nobody will read.
func setGregaleDeadline(header http.Header, remaining time.Duration) {
	ms := max(remaining.Milliseconds(), 1)
	if own := parseGregaleDeadlineMs(header.Values(GregaleDeadlineHeader)); own > 0 && own < ms {
		ms = own
	}
	header.Set(GregaleDeadlineHeader, strconv.FormatInt(ms, 10))
}
