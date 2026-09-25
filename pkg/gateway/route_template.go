package gateway

import (
	"strings"

	"github.com/google/uuid"
)

// inferredObservedPath removes common identifiers before a request path is
// admitted as a metric label. This is a conservative fallback for apps without
// a declared route; a declared OpenAPI or app route takes precedence.
// Unrecognised slugs stay literal and remain subject to routeLabelSet's cap.
func inferredObservedPath(path string) string {
	if path == "" || !strings.HasPrefix(path, "/") {
		return otherRouteLabel
	}
	parts := strings.Split(path, "/")
	// A single numeric top-level route may be a static API version or year;
	// require a preceding resource segment before inferring an identifier.
	for i := 2; i < len(parts); i++ {
		if observedIdentifier(parts[i]) {
			parts[i] = "{id}"
		}
	}
	path = strings.Join(parts, "/")
	// The telemetry schema caps the method+path label at 256 bytes. Keep
	// overlong, attacker-controlled paths out of both Prometheus and Postgres.
	if len(path) > 248 {
		return otherRouteLabel
	}
	return path
}

func observedIdentifier(segment string) bool {
	if segment == "" {
		return false
	}
	if len(segment) == 36 {
		if _, err := uuid.Parse(segment); err == nil {
			return true
		}
	}
	digits := true
	hex := len(segment) >= 16
	for _, ch := range segment {
		if ch < '0' || ch > '9' {
			digits = false
		}
		if !isObservedHexDigit(ch) {
			hex = false
		}
	}
	return digits || hex
}

func isObservedHexDigit(ch rune) bool {
	return (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')
}
