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

// GregaleRequestDeadlineHeader carries the platform-signed absolute deadline
// of a participating managed HTTP chain (ADR-570). It is opaque: forward it
// unchanged, never edit or build one. The gateway verifies it before a wake.
const GregaleRequestDeadlineHeader = "X-Gregale-Request-Deadline"

// gregaleRequestDeadlineMaxLen bounds an accepted signed deadline token.
const gregaleRequestDeadlineMaxLen = 2048

type gregaleReleaseContextKey struct{}

type gregaleRequestDeadlineContextKey struct{}

// WithGregaleRequestDeadline returns a context carrying the current request's
// signed deadline token. An empty or malformed token is ignored.
func WithGregaleRequestDeadline(ctx context.Context, token string) context.Context {
	token = cleanGregaleRequestDeadline(token)
	if token == "" {
		return ctx
	}
	return context.WithValue(ctx, gregaleRequestDeadlineContextKey{}, token)
}

// GregaleRequestDeadlineFromContext returns the signed deadline token the
// middleware captured for the current request, if any. Never edit its bytes.
func GregaleRequestDeadlineFromContext(ctx context.Context) (string, bool) {
	token, ok := ctx.Value(gregaleRequestDeadlineContextKey{}).(string)
	return token, ok && token != ""
}

// cleanGregaleRequestDeadline accepts the token's opaque URL-safe form only,
// as the Node and Python SDKs do.
func cleanGregaleRequestDeadline(token string) string {
	if token == "" || len(token) > gregaleRequestDeadlineMaxLen {
		return ""
	}
	for _, c := range token {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-') {
			return ""
		}
	}
	return token
}

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

// GregaleReleaseMiddleware captures the release selected by Gregale, the
// request's signed chain deadline (X-Gregale-Request-Deadline) and the time it
// has left (X-Faas-Deadline-Ms) on an inbound HTTP request and makes them
// available to outbound service calls made with that request's context.
// Deadlines are not captured for a WebSocket upgrade: long-lived sessions are
// not part of an ordinary request chain (ADR-570).
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
		if !isWebSocketUpgrade(r) {
			if tokens := r.Header.Values(GregaleRequestDeadlineHeader); len(tokens) == 1 {
				r = r.WithContext(WithGregaleRequestDeadline(r.Context(), tokens[0]))
			}
			if ms := parseGregaleDeadlineMs(r.Header.Values(GregaleDeadlineHeader)); ms > 0 {
				r = r.WithContext(WithGregaleDeadline(r.Context(), time.Now().Add(time.Duration(ms)*time.Millisecond)))
			}
		}
		next.ServeHTTP(w, r)
	})
}

// NewGregaleReleaseTransport returns a RoundTripper that forwards the
// request-scoped release, signed chain deadline and the time the request has
// left to managed Gregale service hosts (for example, billing.svc.gregale, or
// a declared *.internal alias). It never forwards a revision pin to a service because
// that pin belongs to the caller app, not the target app. Requests to other
// hosts are left untouched, except that a signed deadline token is removed:
// http.Client calls the transport again at every redirect hop, so the token
// never reaches an external redirect destination.
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
		if len(req.Header.Values(GregaleRequestDeadlineHeader)) == 0 {
			return t.base.RoundTrip(req)
		}
		cloned := req.Clone(req.Context())
		cloned.Header = req.Header.Clone()
		cloned.Header.Del(GregaleRequestDeadlineHeader)
		return t.base.RoundTrip(cloned)
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
	// The request's own token wins over an explicit downstream carrier:
	// replacing it would unlink the call from its parent chain.
	token, ok := GregaleRequestDeadlineFromContext(req.Context())
	if !ok {
		token = cleanGregaleRequestDeadline(singleHeader(cloned.Header, GregaleRequestDeadlineHeader))
	}
	if token != "" {
		cloned.Header.Set(GregaleRequestDeadlineHeader, token)
	} else {
		cloned.Header.Del(GregaleRequestDeadlineHeader)
	}
	if deadline, ok := GregaleDeadlineFromContext(req.Context()); ok {
		setGregaleDeadline(cloned.Header, time.Until(deadline))
	}
	return t.base.RoundTrip(cloned)
}

func isGregaleServiceHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	return strings.HasSuffix(host, ".svc.gregale") || strings.HasSuffix(host, ".internal")
}

func singleHeader(header http.Header, name string) string {
	if values := header.Values(name); len(values) == 1 {
		return values[0]
	}
	return ""
}

func isWebSocketUpgrade(r *http.Request) bool {
	for _, value := range r.Header.Values("Upgrade") {
		if strings.EqualFold(strings.TrimSpace(value), "websocket") {
			return true
		}
	}
	return false
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
