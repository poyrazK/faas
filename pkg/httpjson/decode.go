// Package httpjson provides bounded decoding for JSON responses received over
// HTTP. A response body is untrusted input even when the endpoint is an
// authenticated provider, so callers must supply an endpoint-appropriate cap.
package httpjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// ErrResponseTooLarge indicates that a response body exceeded the caller's
// configured limit before it could be decoded.
var ErrResponseTooLarge = errors.New("httpjson: response body too large")

// Decode reads and decodes exactly one JSON value while bounding the body in
// memory. The extra-byte read makes the limit strict rather than silently
// accepting a truncated body. Trailing non-whitespace JSON is rejected too,
// preventing an upstream from smuggling a second value past the caller.
func Decode(r io.Reader, maxBytes int64, dst any) error {
	if maxBytes <= 0 {
		return fmt.Errorf("httpjson: invalid response limit %d", maxBytes)
	}
	body, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return fmt.Errorf("httpjson: read response body: %w", err)
	}
	if int64(len(body)) > maxBytes {
		return fmt.Errorf("%w: limit=%d bytes", ErrResponseTooLarge, maxBytes)
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("httpjson: trailing JSON value")
		}
		return fmt.Errorf("httpjson: trailing data: %w", err)
	}
	return nil
}
