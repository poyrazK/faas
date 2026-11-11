package api

// NormalizeRequestAnalyticsMethod bounds method cardinality without changing
// the HTTP method forwarded to the application. Empty denotes an aggregate
// without a method; nonstandard HTTP methods share the OTHER analytics group.
func NormalizeRequestAnalyticsMethod(method string) string {
	switch method {
	case "", "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "OTHER":
		return method
	default:
		return "OTHER"
	}
}
