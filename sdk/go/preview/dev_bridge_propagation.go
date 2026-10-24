package faas

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
)

// DevBridgeContextHeader carries request routing authority across explicitly opted-in
// service calls. It never carries the credential that can attach a laptop.
const DevBridgeContextHeader = "X-Gregale-Dev-Session-Context"

type DevBridgeRequestContext struct {
	AccountID string
	SessionID string
	Token     string
}

func (c DevBridgeRequestContext) Encode() string {
	return c.AccountID + "." + c.SessionID + "." + c.Token
}

func parseDevBridgeRequestContext(value string) (DevBridgeRequestContext, error) {
	parts := strings.Split(value, ".")
	if len(parts) != 3 || len(parts[0]) == 0 || len(parts[0]) > 64 {
		return DevBridgeRequestContext{}, errDevBridgeContext
	}
	for _, part := range parts[1:] {
		decoded, err := base64.RawURLEncoding.DecodeString(part)
		if len(part) != 43 || err != nil || len(decoded) != 32 {
			return DevBridgeRequestContext{}, errDevBridgeContext
		}
	}
	return DevBridgeRequestContext{parts[0], parts[1], parts[2]}, nil
}

type devBridgeRequestContextKey struct{}

// DevBridgePropagationMiddleware opts an application into preserving session context.
// Parsing is not authorization; Gregale revalidates the lease at every hop.
func DevBridgePropagationMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if value := r.Header.Get(DevBridgeContextHeader); value != "" {
			if parsed, err := parseDevBridgeRequestContext(value); err == nil {
				r = r.WithContext(context.WithValue(r.Context(), devBridgeRequestContextKey{}, parsed))
			}
		}
		next.ServeHTTP(w, r)
	})
}

// DevBridgePropagationTransport forwards context only to Gregale service discovery
// names. Requests must use the incoming request's context. Explicit headers
// are always removed first, including when calling a third-party API.
type DevBridgePropagationTransport struct{ Base http.RoundTripper }

func (t DevBridgePropagationTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	clone := r.Clone(r.Context())
	clearDevBridgeCredentials(clone.Header)
	clearDevBridgeRequestContext(clone.Header)
	host := strings.ToLower(strings.TrimSuffix(r.URL.Hostname(), "."))
	if c, ok := r.Context().Value(devBridgeRequestContextKey{}).(DevBridgeRequestContext); ok &&
		devBridgeServiceContextHost(host) {
		clone.Header.Set(DevBridgeContextHeader, c.Encode())
	}
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(clone)
}

func devBridgeServiceContextHost(host string) bool {
	for _, suffix := range []string{".svc.gregale", ".internal"} {
		if label, ok := strings.CutSuffix(host, suffix); ok && label != "" && !strings.Contains(label, ".") {
			return true
		}
	}
	return false
}

var errDevBridgeContext = errors.New("invalid development session context")

func clearDevBridgeCredentials(h http.Header) {
	for key := range h {
		if strings.HasPrefix(strings.ToLower(key), "x-gregale-dev-bridge-") {
			delete(h, key)
		}
	}
}
func clearDevBridgeRequestContext(h http.Header) {
	for key := range h {
		if strings.EqualFold(key, DevBridgeContextHeader) {
			delete(h, key)
		}
	}
}
