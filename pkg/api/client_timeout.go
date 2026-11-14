package api

import (
	"net/http"
	"time"
)

// NewClientWithRequestTimeout builds a client with an explicit timeout for
// ordinary API requests and uploads. It sets both the whole-request timeout
// and the response-header wait. Non-positive values retain NewClient defaults.
// Operation-specific clients derived from this client honor the explicit
// budget; caller context deadlines can shorten it. SSE clients keep their
// unbounded body lifetime and inherit the response-header wait instead.
func NewClientWithRequestTimeout(baseURL, token string, timeout time.Duration) *Client {
	c := NewClient(baseURL, token)
	if timeout > 0 {
		c.http.Timeout = timeout
		c.http.Transport = newClientTransportWithHeaderTimeout(timeout)
		c.requestTimeoutExplicit = true
	}
	return c
}

// operationHTTP keeps legacy operation budgets unless the caller explicitly
// selected a request timeout. Copying retains redirects, TLS and wrappers.
func (c *Client) operationHTTP(timeout time.Duration) *http.Client {
	client := *c.http
	if !c.requestTimeoutExplicit {
		client.Timeout = timeout
	}
	return &client
}
