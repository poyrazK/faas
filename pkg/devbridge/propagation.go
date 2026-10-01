package devbridge

import (
	"context"
	"encoding/base64"
	"net/http"
	"strings"
)

// ContextHeader carries request routing authority across explicitly opted-in
// service calls. It never carries the credential that can attach a laptop.
const ContextHeader = "X-Gregale-Dev-Session-Context"

type RequestContext struct {
	AccountID string
	SessionID string
	Token     string
}

func (c RequestContext) Encode() string { return c.AccountID + "." + c.SessionID + "." + c.Token }

func ParseRequestContext(value string) (RequestContext, error) {
	parts := strings.Split(value, ".")
	if len(parts) != 3 || len(parts[0]) == 0 || len(parts[0]) > 64 {
		return RequestContext{}, ErrUnauthorized
	}
	for _, part := range parts[1:] {
		decoded, err := base64.RawURLEncoding.DecodeString(part)
		if len(part) != 43 || err != nil || len(decoded) != 32 {
			return RequestContext{}, ErrUnauthorized
		}
	}
	return RequestContext{parts[0], parts[1], parts[2]}, nil
}

type requestContextKey struct{}

// PropagationMiddleware opts an application into preserving session context.
// Parsing is not authorization; Gregale revalidates the lease at every hop.
func PropagationMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if value := r.Header.Get(ContextHeader); value != "" {
			if parsed, err := ParseRequestContext(value); err == nil {
				r = r.WithContext(context.WithValue(r.Context(), requestContextKey{}, parsed))
			}
		}
		next.ServeHTTP(w, r)
	})
}

// PropagationTransport forwards context only to Gregale service discovery
// names. Requests must use the incoming request's context. Explicit headers
// are always removed first, including when calling a third-party API.
type PropagationTransport struct{ Base http.RoundTripper }

func (t PropagationTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	clone := r.Clone(r.Context())
	ClearCredentials(clone.Header)
	ClearRequestContext(clone.Header)
	host := strings.ToLower(strings.TrimSuffix(r.URL.Hostname(), "."))
	if c, ok := r.Context().Value(requestContextKey{}).(RequestContext); ok &&
		serviceContextHost(host) {
		clone.Header.Set(ContextHeader, c.Encode())
	}
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(clone)
}

func serviceContextHost(host string) bool {
	for _, suffix := range []string{".svc.gregale", ".internal"} {
		if label, ok := strings.CutSuffix(host, suffix); ok && label != "" && !strings.Contains(label, ".") {
			return true
		}
	}
	return false
}
