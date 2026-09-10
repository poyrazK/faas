package internal

import "net/http"

// ApplyResponseHeaders copies the handler's response headers and supplies the
// binary-safe default only when the handler did not declare a content type.
// http.Header.Get is case-insensitive, so lower-case Node header keys are
// preserved just as canonical keys are.
func ApplyResponseHeaders(dst http.Header, src map[string]string) {
	for key, value := range src {
		dst.Set(key, value)
	}
	if dst.Get("Content-Type") == "" {
		dst.Set("Content-Type", "application/octet-stream")
	}
}
