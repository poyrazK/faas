package api

// Request-scoped response headers shared by the gateway and optional edge
// adapters. Keep these names stable: a CDN/Worker may need to recover a
// platform response after replacing an origin error page.
const (
	// RequestIDHeader carries the platform correlation id for every request.
	RequestIDHeader = "X-Faas-Request-Id"
	// ErrorCodeHeader identifies a platform-owned error independently of the
	// response body. Edge adapters use it to distinguish a Gregale timeout
	// from a genuine CDN/origin failure.
	ErrorCodeHeader = "X-Faas-Error-Code"
)
