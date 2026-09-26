package faas

import (
	"context"
	"net/http"
	"strings"
)

// GregaleRevisionHeader identifies an exact deployment for one app. It is
// deliberately not propagated to downstream services: revision pins are
// scoped to the app that received them.
const GregaleRevisionHeader = "X-Gregale-Revision"

// GregaleReleaseHeader identifies an immutable project deployment graph.
const GregaleReleaseHeader = "X-Gregale-Release"

type gregaleReleaseContextKey struct{}

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

// GregaleReleaseMiddleware captures the release selected by Gregale on an
// inbound HTTP request and makes it available to outbound service calls made
// with that request's context.
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
		next.ServeHTTP(w, r)
	})
}

// NewGregaleReleaseTransport returns a RoundTripper that forwards the
// request-scoped release to managed Gregale service hosts (for example,
// billing.svc.gregale). It never forwards a revision pin to a service because
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
