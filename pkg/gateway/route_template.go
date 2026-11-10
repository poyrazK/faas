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
		parts[i] = inferredSegment(parts[i])
	}
	path = strings.Join(parts, "/")
	// The telemetry schema caps the method+path label at 256 bytes. Keep
	// overlong, attacker-controlled paths out of both Prometheus and Postgres.
	if len(path) > 248 {
		return otherRouteLabel
	}
	return path
}

// observedRouteLabel is shared by audit and independent discovery. Untrusted
// path bytes must not turn a financially critical outbox event into a
// permanently rejected delivery at the receiver.
func observedRouteLabel(method, path string) string {
	if path == otherRouteLabel || method == "" || len(method) > 16 ||
		len(method)+1+len(path) > 256 || strings.ContainsAny(path, "?#\x00\r\n\t") {
		return otherRouteLabel
	}
	return method + " " + path
}

// inferredSegment templates one path segment: an identifier becomes {id},
// a calendar date {date}, and a file named by an identifier keeps its
// extension ({id}.json.gz), so one-off export and object downloads share a
// label (ADR-20261028110044 amendment 7).
func inferredSegment(segment string) string {
	if observedIdentifier(segment) {
		return "{id}"
	}
	if observedDate(segment) {
		return "{date}"
	}
	if stem, ext, ok := strings.Cut(segment, "."); ok && stem != "" && ext != "" && observedIdentifier(stem) {
		return "{id}." + ext
	}
	return segment
}

// observedDate matches an ISO calendar date segment (2026-11-07).
func observedDate(segment string) bool {
	if len(segment) != 10 || segment[4] != '-' || segment[7] != '-' {
		return false
	}
	for i, ch := range segment {
		if i != 4 && i != 7 && (ch < '0' || ch > '9') {
			return false
		}
	}
	return true
}

// observedPrefixedIdentifier matches a typed identifier such as qry_<hex>,
// cus_<base62> or evt-<ulid>: a short lowercase type prefix, then a token of
// at least 10 letters and digits that contains a digit.
func observedPrefixedIdentifier(segment string) bool {
	cut := strings.IndexAny(segment, "_-")
	if cut < 1 || cut > 8 {
		return false
	}
	for _, ch := range segment[:cut] {
		if ch < 'a' || ch > 'z' {
			return false
		}
	}
	token := segment[cut+1:]
	if len(token) < 10 {
		return false
	}
	digit := false
	for _, ch := range token {
		switch {
		case ch >= '0' && ch <= '9':
			digit = true
		case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z':
		default:
			return false
		}
	}
	return digit
}

func observedIdentifier(segment string) bool {
	if observedPrefixedIdentifier(segment) {
		return true
	}
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
