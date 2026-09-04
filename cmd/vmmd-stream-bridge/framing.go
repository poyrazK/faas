// Package main (vmmd-stream-bridge) per-stream framing switch
// (ADR-126, issue / G19). Two framing paths share the CR/LF
// sanitization + header parsing so the vmmd→guest envelope stays
// consistent across both:
//   - h1  : legacy H1+chunked path (today's writeH1RequestHead +
//     writeChunkedBody). app_protocol=http1 (default) or
//     explicit rollback via FAAS_BRIDGE_PROTOCOL=h1.
//   - h2c : new H2C terminator path (h2c_terminator.go). Prior-
//     knowledge HTTP/2 frames to the guest. app_protocol in
//     {http2, grpc}.
//
// The framing decision is per-stream (per inbound H2C request),
// not per-bridge-process: each handler invocation reads its own
// FAAS_BRIDGE_PROTOCOL env var (mirrors the per-request
// FAAS_STREAM_BRIDGE_VERSION lookup at pkg/vmmdgrpc/forward.go:565-570).
// Two streams for the same app can ride different framing only
// if the operator mutates the env var mid-flight — which is the
// rollback story (ADR-126 §Decision 7).
package main

import (
	"net/http"
	"os"
	"strconv"
	"strings"
)

// These headers are private to the vmmd↔bridge H2C hop. They carry request
// metadata that used to live in the bridge process environment. Keeping them
// on each request makes a single bridge safe for concurrent invocations.
const (
	bridgeRequestMarkerHeader   = "X-Faas-Bridge-Persistent"
	bridgeRequestProtocolHeader = "X-Faas-Bridge-Protocol"
	bridgeRequestPortHeader     = "X-Faas-Bridge-Port"
	bridgeRequestHostHeader     = "X-Faas-Bridge-Host"
)

// bridgeFraming is the per-stream framing selector (ADR-126).
// Closed set `{h1, h2c}` — anything else falls back to h1 so a
// misconfigured operator gets the legacy path instead of a crash.
type bridgeFraming string

const (
	framingH1  bridgeFraming = "h1"
	framingH2C bridgeFraming = "h2c"
)

// String implements fmt.Stringer so structured-log fields carry
// the operator-facing name (ADR-127 §D3 Layer 7 — the
// framing-selection slog line in main.go::newHandler). Returning
// the underlying string is intentional: the type's primitive
// representation IS the operator-facing representation.
func (f bridgeFraming) String() string { return string(f) }

// currentBridgeFraming returns the per-stream framing selection
// from FAAS_BRIDGE_PROTOCOL. The lookup is per-request, NOT
// captured at package init, mirroring the FAAS_STREAM_BRIDGE_VERSION
// pattern at pkg/vmmdgrpc/forward.go:560-570 (the ADR-028 amendment
// promises a no-deploy rollback story by reading the env at every
// RPC). Empty / unknown values fall back to h1 — the load-bearing
// zero-behavior-change baseline for legacy callers and the
// "unknown app_protocol → fall through to legacy" defense.
//
// Thin wrapper around currentBridgeFramingFrom so callers that
// already read FAAS_BRIDGE_PROTOCOL for their own purposes (the
// framing-selection slog line in main.go::newHandler) can pass the
// already-read value rather than triggering a second syscall. The
// default-zero call shape stays available for tests + the dispatch
// sites that don't need the raw env.
func currentBridgeFraming() bridgeFraming {
	return currentBridgeFramingFrom(os.Getenv("FAAS_BRIDGE_PROTOCOL"))
}

// currentBridgeFramingFrom is the testable seam — the dispatch table
// is small enough to live next to the lookup, but pulling it out
// lets a test assert the h2c / h1 / fallback matrix without poking
// the process env (which is host-global and breaks parallel tests).
func currentBridgeFramingFrom(env string) bridgeFraming {
	switch env {
	case "h2c":
		return framingH2C
	default:
		// Empty, "h1", or any other value → h1 path. Logging
		// happens in newHandler's per-request framing-selection
		// line so operators can correlate the env value with
		// the framing actually used.
		return framingH1
	}
}

// headerEntry is one (name, value) pair from FAAS_BRIDGE_HEADERS.
// Lifted verbatim from main.go so the h1 and h2c framing paths
// share the same parser.
type headerEntry struct {
	Name  string
	Value string
}

// parseHeaders splits a newline-separated `k=v\nk=v` string into
// header entries. Newline is the separator because HTTP/1.1 field-
// values may not contain CR or LF (RFC 9110 §5.5 — obs-fold was
// removed by the obsoletion of RFC 7230). Comma is NOT a safe
// separator: real headers like `Accept: text/html, application/json`
// and `Cache-Control: no-cache, no-store` carry commas in their
// VALUES. Split on the FIRST `=` so values may also contain `=`.
// Empty names are dropped. Names are returned verbatim (the
// vmmd caller already lower-cased or canon-cased them via the
// original `textproto.MIMEHeader`); we pass through unchanged
// since the H1 wire is case-insensitive on header names.
func parseHeaders(s string) []headerEntry {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, "\n")
	out := make([]headerEntry, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			continue
		}
		eq := strings.IndexByte(p, '=')
		if eq <= 0 {
			continue
		}
		out = append(out, headerEntry{
			Name:  p[:eq],
			Value: p[eq+1:],
		})
	}
	return out
}

// sanitizeCRLF strips CR, LF, and NUL bytes from a string destined
// for the H1 wire. Defense-in-depth: vmmd already strips these in
// streamBridgeEnv (pkg/vmmdgrpc/forward.go), but the bridge is a
// stand-alone binary that may be invoked from other surfaces
// (tests, future operator override, `FAAS_BRIDGE_*=value` env-set
// on a misconfigured host). Stripping again here means a hostile
// or buggy caller cannot smuggle a header line into the trusted
// inner envelope via FAAS_BRIDGE_HOST / FAAS_BRIDGE_HEADERS.
// CR/LF/NUL are illegal in HTTP/1.1 field-values (RFC 9110 §5.5);
// stripping is lossless for legitimate input.
//
// Used by BOTH the h1 framing path (main.go::writeH1RequestHead)
// AND the h2c framing path (h2c_terminator.go::writeRequestHead)
// — single source of truth for the trusted inner envelope
// sanitization (ADR-126 §Decision 1).
func sanitizeCRLF(s string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\r' || c == '\n' || c == 0 {
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

func bridgeRequestUsesWireMetadata(r *http.Request) bool {
	return r != nil && r.Header.Get(bridgeRequestMarkerHeader) == "1"
}

func bridgeRequestPort(r *http.Request, fallback uint16) uint16 {
	if r == nil || !bridgeRequestUsesWireMetadata(r) {
		return fallback
	}
	parsed, err := strconv.ParseUint(r.Header.Get(bridgeRequestPortHeader), 10, 16)
	if err != nil || parsed == 0 {
		return fallback
	}
	return uint16(parsed)
}

func bridgeRequestHost(r *http.Request, fallback string) string {
	if r == nil {
		return fallback
	}
	if bridgeRequestUsesWireMetadata(r) {
		if host := r.Header.Get(bridgeRequestHostHeader); host != "" {
			return sanitizeCRLF(host)
		}
	}
	if bridgeRequestUsesWireMetadata(r) && r.Host != "" && r.Host != "unix" {
		return sanitizeCRLF(r.Host)
	}
	return fallback
}

func bridgeRequestHeaders(r *http.Request) []headerEntry {
	if r == nil || !bridgeRequestUsesWireMetadata(r) {
		return parseHeaders(os.Getenv("FAAS_BRIDGE_HEADERS"))
	}
	var headers []headerEntry
	for name, values := range r.Header {
		if isBridgeRequestHeader(name) || isHopByHopHeader(name) || strings.EqualFold(name, "Host") {
			continue
		}
		for _, value := range values {
			headers = append(headers, headerEntry{Name: sanitizeCRLF(name), Value: sanitizeCRLF(value)})
		}
	}
	return headers
}

func isBridgeRequestHeader(name string) bool {
	switch {
	case strings.EqualFold(name, bridgeRequestMarkerHeader):
		return true
	case strings.EqualFold(name, bridgeRequestProtocolHeader):
		return true
	case strings.EqualFold(name, bridgeRequestPortHeader):
		return true
	case strings.EqualFold(name, bridgeRequestHostHeader):
		return true
	default:
		return false
	}
}
