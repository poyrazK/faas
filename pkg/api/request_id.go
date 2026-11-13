package api

import "net/http"

// WithRequestID attaches validated client-only error correlation metadata.
// It adds neither a JSON field nor a response header. Invalid values clear it.
func (p *Problem) WithRequestID(id string) *Problem {
	p.clientRequestID = ""
	if opaqueRequestID(id) {
		p.clientRequestID = id
	}
	return p
}

// RequestID returns the SDK-retained correlation ID, if present.
func (p *Problem) RequestID() string {
	if p == nil {
		return ""
	}
	return p.clientRequestID
}

// ResponseRequestID returns a single opaque platform correlation ID, or an
// empty string for missing, ambiguous or malformed headers. Only 32 hex digits
// or the equivalent hyphenated UUID shape are accepted; values are not trimmed
// or normalized. Header names are matched case-insensitively, including when a
// custom RoundTripper supplies a non-canonical map.
func ResponseRequestID(header http.Header) string {
	var id string
	for name, values := range header {
		if http.CanonicalHeaderKey(name) != RequestIDHeader {
			continue
		}
		for _, value := range values {
			if id != "" || !opaqueRequestID(value) {
				return ""
			}
			id = value
		}
	}
	return id
}

func opaqueRequestID(id string) bool {
	if len(id) != 32 && len(id) != 36 {
		return false
	}
	for i, char := range id {
		if len(id) == 36 && (i == 8 || i == 13 || i == 18 || i == 23) {
			if char != '-' {
				return false
			}
			continue
		}
		if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'f' || char >= 'A' && char <= 'F') {
			return false
		}
	}
	return true
}
