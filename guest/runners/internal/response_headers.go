package internal

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// HeaderValues is a handler response's headers (§4.9). A JSON string value is
// one header line and a list of strings is one line per element, so a
// handler can send several Set-Cookie headers (ADR-999).
type HeaderValues map[string][]string

// UnmarshalJSON accepts a string or a list of strings for each header.
func (h *HeaderValues) UnmarshalJSON(b []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	out := make(HeaderValues, len(raw))
	for name, value := range raw {
		var one string
		if err := json.Unmarshal(value, &one); err == nil {
			out[name] = []string{one}
			continue
		}
		var many []string
		if err := json.Unmarshal(value, &many); err != nil {
			return fmt.Errorf("header %q: want a string or a list of strings", name)
		}
		out[name] = many
	}
	*h = out
	return nil
}

// ApplyResponseHeaders copies the handler's response headers and supplies the
// binary-safe default only when the handler did not declare a content type.
// http.Header.Get is case-insensitive, so lower-case Node header keys are
// preserved just as canonical keys are.
func ApplyResponseHeaders(dst http.Header, src HeaderValues) {
	for key, values := range src {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
	if dst.Get("Content-Type") == "" {
		dst.Set("Content-Type", "application/octet-stream")
	}
}

// RequestHeaders folds a request's headers into the §4.9 envelope's one
// string per name. A repeated header is joined as RFC 9110 §5.3 allows, with
// ", ", and Cookie with "; " (RFC 6265 §5.4), so no value is dropped.
func RequestHeaders(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for name, values := range h {
		if len(values) == 0 {
			continue
		}
		separator := ", "
		if http.CanonicalHeaderKey(name) == "Cookie" {
			separator = "; "
		}
		out[name] = strings.Join(values, separator)
	}
	return out
}
