// White-box tests for the v2 bridge selector that need access to
// unexported symbols (currentStreamBridgeVersion, streamBridgeEnv,
// sanitizeHeaderValue). The other v2 tests in forward_v2_test.go
// run in package vmmdgrpc_test for black-box coverage; this file
// lives in package vmmdgrpc so it can read the per-request lookup
// function directly.
//
// PR #754's medium code review caught that streamBridgeVersion was
// captured at package init, breaking the documented no-deploy
// rollback story. The test here pins the per-request behavior so a
// future refactor that re-introduces init-time capture fails loud.

package vmmdgrpc

import (
	"strings"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

// TestCurrentStreamBridgeVersion_LiveRollback verifies the per-
// request FAAS_STREAM_BRIDGE_VERSION env lookup. Each t.Setenv
// re-reads the env on the next call to currentStreamBridgeVersion();
// the package-level streamBridgeVersion var is NOT consulted here
// (SetStreamBridgeVersion is a separate test-only seam).
func TestCurrentStreamBridgeVersion_LiveRollback(t *testing.T) {
	// Default (no env): v2.
	t.Setenv("FAAS_STREAM_BRIDGE_VERSION", "")
	if got := currentStreamBridgeVersion(); got != "v2" {
		t.Errorf("currentStreamBridgeVersion() with empty env = %q, want %q", got, "v2")
	}

	// Set to v1: must return v1 on the NEXT call, not the original
	// v2 capture. This is the regression the code review caught.
	t.Setenv("FAAS_STREAM_BRIDGE_VERSION", "v1")
	if got := currentStreamBridgeVersion(); got != "v1" {
		t.Errorf("currentStreamBridgeVersion() with v1 env = %q, want %q", got, "v1")
	}

	// Flip back to v2: must return v2. Live rollback is two-way.
	t.Setenv("FAAS_STREAM_BRIDGE_VERSION", "v2")
	if got := currentStreamBridgeVersion(); got != "v2" {
		t.Errorf("currentStreamBridgeVersion() with v2 env = %q, want %q", got, "v2")
	}

	// Garbage value passes through verbatim. The forward.go
	// dispatch site only treats "v2" specially; anything else
	// (including the empty string) falls through to the v1 path.
	// We pin that here so a future refactor that defaults to v2
	// on unknown values is a deliberate change, not an accident.
	t.Setenv("FAAS_STREAM_BRIDGE_VERSION", "v3")
	if got := currentStreamBridgeVersion(); got != "v3" {
		t.Errorf("currentStreamBridgeVersion() with v3 env = %q, want %q (verbatim pass-through)", got, "v3")
	}
}

// TestStreamBridgeVersionVar_DefaultIsV2 pins the package-level
// default for the test seam. SetStreamBridgeVersion is the
// documented override path; the default value (when no env var
// is set and no setter has been called) must be "v2" so that
// unit tests that don't bother to set it still exercise the v2
// path.
func TestStreamBridgeVersionVar_DefaultIsV2(t *testing.T) {
	// We can't t.Setenv here without affecting parallel tests
	// in this package, but the var's initial value is set at
	// package load time and should be "v2". If a future refactor
	// changes the default, this test fires immediately.
	if streamBridgeVersion != "v2" {
		t.Errorf("streamBridgeVersion default = %q, want %q (PR #754 flipped default to v2)", streamBridgeVersion, "v2")
	}
}

// TestStreamBridgeEnv_SanitizesCRLF pins the vmmd-side CRLF
// stripping for FAAS_BRIDGE_HOST / FAAS_BRIDGE_HEADERS / METHOD /
// URL. The bridge writes these values verbatim to the guest TCP
// socket; CR/LF would let a caller smuggle a complete header line
// into the trusted inner envelope (CRLF injection — finding #6
// from PR #754's medium code review). v1's shellQuote() closed
// this hole; v2 must match.
func TestStreamBridgeEnv_SanitizesCRLF(t *testing.T) {
	req := &vmmdpb.ForwardHTTPRequestInit{
		Method:     "POST\r\nX-Injected: bad",
		RequestUri: "/foo",
		Headers: []*vmmdpb.Header{
			{Name: "Host", Value: "evil.com\r\nX-Injected: bad"},
			{Name: "X-Real", Value: "ok\r\nstill-bad"},
			{Name: "User-Agent", Value: "curl/7\rfoo"}, // bare LF only
		},
	}
	env := streamBridgeEnv(req)

	got := map[string]string{}
	for _, kv := range env {
		if i := strings.IndexByte(kv, '='); i > 0 {
			got[kv[:i]] = kv[i+1:]
		}
	}

	// METHOD: CR/LF stripped.
	if strings.ContainsAny(got["FAAS_BRIDGE_METHOD"], "\r\n") {
		t.Errorf("FAAS_BRIDGE_METHOD contains CR/LF: %q", got["FAAS_BRIDGE_METHOD"])
	}
	if got["FAAS_BRIDGE_METHOD"] != "POSTX-Injected: bad" {
		t.Errorf("FAAS_BRIDGE_METHOD = %q, want %q", got["FAAS_BRIDGE_METHOD"], "POSTX-Injected: bad")
	}

	// URL: passes through (no CR/LF).
	if got["FAAS_BRIDGE_URL"] != "/foo" {
		t.Errorf("FAAS_BRIDGE_URL = %q, want %q", got["FAAS_BRIDGE_URL"], "/foo")
	}

	// HOST: CR/LF stripped, header injection blocked.
	if strings.ContainsAny(got["FAAS_BRIDGE_HOST"], "\r\n") {
		t.Errorf("FAAS_BRIDGE_HOST contains CR/LF: %q", got["FAAS_BRIDGE_HOST"])
	}
	if got["FAAS_BRIDGE_HOST"] != "evil.comX-Injected: bad" {
		t.Errorf("FAAS_BRIDGE_HOST = %q, want %q", got["FAAS_BRIDGE_HOST"], "evil.comX-Injected: bad")
	}

	// HEADERS list: each entry's value is stripped; the entry
	// itself is preserved with the colon-not-newline format
	// (the bridge's parseHeaders splits on \n, so an embedded
	// \n would split one logical header into two).
	headers := got["FAAS_BRIDGE_HEADERS"]
	if strings.Contains(headers, "\r\n") {
		t.Errorf("FAAS_BRIDGE_HEADERS contains CRLF: %q", headers)
	}
	// X-Real: CR/LF stripped — value collapsed.
	if !strings.Contains(headers, "X-Real=okstill-bad") {
		t.Errorf("FAAS_BRIDGE_HEADERS missing sanitized X-Real: %q", headers)
	}
	// User-Agent: bare LF stripped (no \r).
	if !strings.Contains(headers, "User-Agent=curl/7foo") {
		t.Errorf("FAAS_BRIDGE_HEADERS missing sanitized User-Agent: %q", headers)
	}
	// Host is NOT in FAAS_BRIDGE_HEADERS — it's lifted into its
	// own env var above. If it shows up here, the lift logic broke.
	if strings.Contains(headers, "Host=") {
		t.Errorf("FAAS_BRIDGE_HEADERS unexpectedly contains Host: %q", headers)
	}
}

// TestStreamBridgeEnv_AppProtocolWiring pins the FAAS_BRIDGE_PROTOCOL
// derivation in streamBridgeEnv (ADR-126 §Decision 1, Atomic 4).
// The closed-set `{h1, h2c}` must mirror app_protocol ∈ {http1, http2,
// grpc}; unknown / empty values fall back to h1 so legacy callers
// keep working. The test exercises the full env-var projection so a
// future refactor that drops FAAS_BRIDGE_PROTOCOL fires loud here.
func TestStreamBridgeEnv_AppProtocolWiring(t *testing.T) {
	t.Setenv("FAAS_BRIDGE_PROTOCOL", "")
	cases := []struct {
		name        string
		appProtocol string // ForwardHTTPRequestInit.AppProtocol
		wantProto   string // expected FAAS_BRIDGE_PROTOCOL value
	}{
		{name: "empty-defaults-h1", appProtocol: "", wantProto: "h1"},
		{name: "http1-stays-h1", appProtocol: "http1", wantProto: "h1"},
		{name: "http2-promotes-h2c", appProtocol: "http2", wantProto: "h2c"},
		{name: "grpc-promotes-h2c", appProtocol: "grpc", wantProto: "h2c"},
		{name: "unknown-falls-through-h1", appProtocol: "websocket", wantProto: "h1"},
		{name: "garbage-falls-through-h1", appProtocol: "HTTP2", wantProto: "h1"}, // case-sensitive closed-set
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := &vmmdpb.ForwardHTTPRequestInit{
				Method:      "GET",
				RequestUri:  "/",
				AppProtocol: c.appProtocol,
			}
			env := streamBridgeEnv(req)

			got := map[string]string{}
			for _, kv := range env {
				if i := strings.IndexByte(kv, '='); i > 0 {
					got[kv[:i]] = kv[i+1:]
				}
			}
			if got["FAAS_BRIDGE_PROTOCOL"] != c.wantProto {
				t.Errorf("FAAS_BRIDGE_PROTOCOL for app_protocol=%q = %q, want %q",
					c.appProtocol, got["FAAS_BRIDGE_PROTOCOL"], c.wantProto)
			}
		})
	}
}

// appProtocolToBridgeProtocol_RoundTripTable is the closed-set
// exhaustive sweep for the translator itself. StreamBridgeEnv's test
// above pins the env-var projection; this one pins the translator
// function in isolation so a future refactor that swaps the
// switch-statement for a map can be reviewed for closed-set equality.
func TestAppProtocolToBridgeProtocol_ClosedSet(t *testing.T) {
	cases := map[string]string{
		"":      "h1", // legacy callers (no AppProtocol field)
		"http1": "h1",
		"http2": "h2c",
		"grpc":  "h2c",
		// Anything outside the closed-set falls through to h1 —
		// apid rejects these at the customer-intent layer
		// (pkg/api/limits.go::AppProtocol validator), so this
		// path is the defense-in-depth rather than the
		// load-bearing check.
		"http3":       "h1",
		"HTTP2":       "h1", // case-sensitive
		"GRPC":        "h1",
		" websocket ": "h1",
	}
	for in, want := range cases {
		if got := appProtocolToBridgeProtocol(in); got != want {
			t.Errorf("appProtocolToBridgeProtocol(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStreamBridgeProtocol_OperatorOverride(t *testing.T) {
	req := &vmmdpb.ForwardHTTPRequestInit{AppProtocol: "grpc"}
	t.Setenv("FAAS_BRIDGE_PROTOCOL", "h1")
	if got := streamBridgeProtocol(req); got != "h1" {
		t.Fatalf("operator h1 override = %q, want h1", got)
	}
	t.Setenv("FAAS_BRIDGE_PROTOCOL", "not-a-protocol")
	if got := streamBridgeProtocol(req); got != "h1" {
		t.Fatalf("invalid operator override = %q, want safe h1", got)
	}
	t.Setenv("FAAS_BRIDGE_PROTOCOL", "")
	if got := streamBridgeProtocol(req); got != "h2c" {
		t.Fatalf("empty override with grpc = %q, want h2c", got)
	}
}

// TestSanitizeHeaderValue_EmptyAndWhitespace pins the corner
// cases of sanitizeHeaderValue (empty, no-control, only-CRLF,
// only-NUL). Combined with TestSanitizeCRLF in
// cmd/vmmd-stream-bridge/main_test.go (the bridge-side equivalent),
// this pins the vmmd-side sanitization layer.
func TestSanitizeHeaderValue_EmptyAndWhitespace(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: ""},
		{name: "no-control", in: "hello world", want: "hello world"},
		{name: "bare-LF", in: "evil\ninjected", want: "evilinjected"},
		{name: "bare-CR", in: "evil\rinjected", want: "evilinjected"},
		{name: "CRLF", in: "evil\r\ninjected", want: "evilinjected"},
		{name: "NUL-truncation", in: "real\x00fake", want: "realfake"},
		{name: "multiple-LFs", in: "a\nb\nc", want: "abc"},
		{name: "leading-CRLF", in: "\r\nX", want: "X"},
		{name: "trailing-CRLF", in: "X\r\n", want: "X"},
		{name: "only-CRLF", in: "\r\n", want: ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sanitizeHeaderValue(c.in); got != c.want {
				t.Errorf("sanitizeHeaderValue(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
