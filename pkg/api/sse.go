package api

import "net/http"

// sseClient returns the HTTP client used for SSE streams. It
// overrides the SDK's 30s default — a typical apid log stream is
// long-lived (heartbeat every 15s) and a 30s timeout would terminate
// it prematurely.
//
// Callers MUST consume the returned *http.Response.Body and call
// Close on EOF or context cancellation; otherwise the underlying
// goroutine in cmd/apid/handlers_ext.go leaks.
//
// No test ever relies on the timeout being infinite — we only need
// it to be longer than the 30s default so a quiet stream isn't
// killed by an idle-disconnect. Set to 0 (no timeout) so any
// context-aware HTTP/2 keepalive handles the disconnect.
func (c *Client) sseClient() *http.Client {
	// Reuse the default HTTP client but reset its timeout to 0. This
	// shares the *Transport (TLS session, dialer) so a session reuse
	// across API calls does not waste a TLS handshake on every SSE
	// open.
	return &http.Client{
		Timeout:   0,
		Transport: c.http.Transport,
	}
}
