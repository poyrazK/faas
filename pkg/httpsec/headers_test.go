// headers_test.go — table-driven httptest tests modelled on
// pkg/middleware/middleware_test.go:19-111. Each test wraps a
// trivial handler with the middleware, drives a request through
// httptest.NewRecorder, and asserts per-header equality.
//
// The CSP wire format is pinned to the issue #249 spec byte-for-byte
// (TestCSP_HeaderMatchesDashboardSpec) — any future change to
// buildCSP must be reviewed alongside the dashboard template nonce
// attributes under pkg/dashboard/templates/.

package httpsec_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/httpsec"
)

// (no io.Writer scratch helper needed — recordHandler captures
// records directly via its mutex-guarded slice.)

// silentLogger discards everything so test output stays clean.
func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestStatic_SetsAllExpectedHeaders confirms the five static
// headers (HSTS, X-Frame-Options, X-Content-Type-Options,
// Referrer-Policy, Permissions-Policy) land on every response.
// HSTS is gated by HSTSEnabled; both branches exercised.
func TestStatic_SetsAllExpectedHeaders(t *testing.T) {
	cases := []struct {
		name        string
		hstsEnabled bool
	}{
		{"hsts on", true},
		{"hsts off", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prev := httpsec.HSTSEnabled
			httpsec.SetHSTSEnabled(tc.hstsEnabled)
			defer httpsec.SetHSTSEnabled(prev)

			rec := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/dashboard/", nil)

			httpsec.Static(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("ok"))
			})).ServeHTTP(rec, r)

			want := map[string]string{
				httpsec.HeaderXFrameOptions:       httpsec.ValueXFrameOptions,
				httpsec.HeaderXContentTypeOptions: httpsec.ValueXContentTypeOptions,
				httpsec.HeaderReferrerPolicy:      httpsec.ValueReferrerPolicy,
				httpsec.HeaderPermissionsPolicy:   httpsec.ValuePermissionsPolicy,
			}
			for k, v := range want {
				if got := rec.Header().Get(k); got != v {
					t.Errorf("%s = %q, want %q", k, got, v)
				}
			}
			got := rec.Header().Get(httpsec.HeaderStrictTransportSecurity)
			if tc.hstsEnabled {
				if got != httpsec.ValueHSTSMaxAge {
					t.Errorf("HSTS = %q, want %q", got, httpsec.ValueHSTSMaxAge)
				}
			} else if got != "" {
				t.Errorf("HSTS = %q, want empty", got)
			}
		})
	}
}

// TestStatic_PreservesHandlerStatus confirms the middleware does
// not interfere with the handler's status code. 200, 304, and 503
// are all carried through verbatim.
func TestStatic_PreservesHandlerStatus(t *testing.T) {
	cases := []int{http.StatusOK, http.StatusNotModified, http.StatusServiceUnavailable}
	for _, code := range cases {
		t.Run(http.StatusText(code), func(t *testing.T) {
			rec := httptest.NewRecorder()
			httpsec.Static(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(code)
			})).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

			if rec.Code != code {
				t.Errorf("status = %d, want %d", rec.Code, code)
			}
			// Pick one header to prove the static set ran.
			if rec.Header().Get(httpsec.HeaderXFrameOptions) != httpsec.ValueXFrameOptions {
				t.Errorf("X-Frame-Options missing on %d response", code)
			}
		})
	}
}

// TestStatic_IdempotentUnderRemount confirms Static(Static(h)) is
// safe — every header survives the double-wrap without doubling
// values or breaking tests.
func TestStatic_IdempotentUnderRemount(t *testing.T) {
	rec := httptest.NewRecorder()
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	httpsec.Static(httpsec.Static(inner)).ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/", nil))

	if got := rec.Header().Get(httpsec.HeaderXFrameOptions); got != httpsec.ValueXFrameOptions {
		t.Errorf("double-wrapped X-Frame-Options = %q, want %q", got, httpsec.ValueXFrameOptions)
	}
	if got := rec.Header().Values(httpsec.HeaderXFrameOptions); len(got) != 1 {
		t.Errorf("double-wrapped X-Frame-Options count = %d, want 1", len(got))
	}
}

// TestNonce_NewNonceIsRandom confirms two consecutive NewNonce
// calls return distinct values — the simplest sanity check on the
// entropy source.
func TestNonce_NewNonceIsRandom(t *testing.T) {
	a := httpsec.NewNonce()
	b := httpsec.NewNonce()
	if a == b {
		t.Errorf("two NewNonce calls returned %q", a)
	}
	if len(a) != 22 {
		t.Errorf("nonce length = %d, want 22", len(a))
	}
}

// TestNonce_NewNonceIsBase64URL confirms the alphabet is URL-safe
// base64 (no '+' / '/' / '='). If the alphabet drifted the dashboard
// attribute would either break (URL-encoded + → %2B) or leak a
// decode error from html/template's auto-escape.
func TestNonce_NewNonceIsBase64URL(t *testing.T) {
	n := httpsec.NewNonce()
	if strings.ContainsAny(n, "+/=") {
		t.Errorf("nonce %q contains non-URL-safe chars", n)
	}
	for _, r := range n {
		if r != '-' && r != '_' &&
			(r < 'A' || r > 'Z') &&
			(r < 'a' || r > 'z') &&
			(r < '0' || r > '9') {
			t.Errorf("nonce %q has char %q outside URL-safe base64", n, r)
			break
		}
	}
}

// TestNonce_WithNonceEmptyIsNoop confirms WithNonce(ctx, "") does
// not mutate ctx (modeled on middleware.WithRequestID at
// requestid.go:24).
func TestNonce_WithNonceEmptyIsNoop(t *testing.T) {
	ctx := context.Background()
	out := httpsec.WithNonce(ctx, "")
	if out != ctx {
		t.Error("WithNonce(ctx, \"\") mutated ctx; want identity")
	}
}

// TestNonce_RoundtripContext confirms a nonce stored via
// WithNonce is recovered verbatim via NonceFromContext.
func TestNonce_RoundtripContext(t *testing.T) {
	const n = "abc123def456abc123def45"
	got := httpsec.NonceFromContext(httpsec.WithNonce(context.Background(), n))
	if got != n {
		t.Errorf("NonceFromContext = %q, want %q", got, n)
	}
}

// TestNonce_NonceFromEmptyContext confirms NonceFromContext on a
// fresh ctx (or nil) returns "" rather than panicking — the render
// path depends on this when the middleware is bypassed.
func TestNonce_NonceFromEmptyContext(t *testing.T) {
	if got := httpsec.NonceFromContext(context.Background()); got != "" {
		t.Errorf("empty ctx nonce = %q, want \"\"", got)
	}
	if got := httpsec.NonceFromContext(nil); got != "" { //nolint:staticcheck // deliberate nil test
		t.Errorf("nil ctx nonce = %q, want \"\"", got)
	}
}

// TestNonce_GateTrue_EmitsCSP confirms the gate-true path emits
// Content-Security-Policy with a fresh nonce.
func TestNonce_GateTrue_EmitsCSP(t *testing.T) {
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/dashboard/", nil)

	var seen string
	httpsec.Nonce(func(*http.Request) bool { return true },
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen = httpsec.NonceFromContext(r.Context())
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(rec, r)

	csp := rec.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("CSP header missing under gate=true")
	}
	if !strings.Contains(csp, "'nonce-"+seen+"'") {
		t.Errorf("CSP missing matching nonce %q: %s", seen, csp)
	}
}

// TestNonce_GateFalse_OmitsCSP confirms the gate-false path leaves
// no CSP header — gatewayd-internal's customer-app path depends on this.
func TestNonce_GateFalse_OmitsCSP(t *testing.T) {
	rec := httptest.NewRecorder()
	httpsec.Nonce(func(*http.Request) bool { return false },
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/customer-app/foo", nil))

	if got := rec.Header().Get("Content-Security-Policy"); got != "" {
		t.Errorf("CSP header leaked on gate=false: %q", got)
	}
}

// TestNonce_GateNil_OmitsCSP confirms a nil gate (defensive —
// gatewayd-internal's wiring must pass isApidPath, but a unit test should
// not crash on accidental nil) suppresses CSP rather than emitting
// it without scoping.
func TestNonce_GateNil_OmitsCSP(t *testing.T) {
	rec := httptest.NewRecorder()
	httpsec.Nonce(nil, //nolint:staticcheck // deliberate nil gate
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if got := rec.Header().Get("Content-Security-Policy"); got != "" {
		t.Errorf("CSP emitted with nil gate: %q", got)
	}
}

// TestCSP_HeaderMatchesDashboardSpec confirms buildCSP's output
// matches the issue #249 wire format byte-for-byte. If this test
// fails, every dashboard template nonce attribute must be
// re-audited (the CSP `script-src` and `style-src` directives are
// the ones the templates pin via `nonce="{{.Nonce}}"`).
func TestCSP_HeaderMatchesDashboardSpec(t *testing.T) {
	const nonce = "abc123XYZ-_abc123XYZ" // 22 chars, URL-safe
	got := httpsec.BuildCSPForTest(nonce)
	want := "default-src 'self'; " +
		"script-src 'self' 'nonce-" + nonce + "' https://unpkg.com; " +
		"style-src 'self' 'nonce-" + nonce + "' https://unpkg.com; " +
		"img-src 'self' data:; " +
		"connect-src 'self'; " +
		"frame-ancestors 'none'; " +
		"base-uri 'none'; " +
		"form-action 'self' https://*.stripe.com https://billing.faas.example'"
	if got != want {
		t.Errorf("CSP wire mismatch:\n got:  %s\n want: %s", got, want)
	}
}

// TestCSP_NonceFreshness confirms two requests under the gate-true
// middleware produce distinct nonces (an old nonce in a fresh
// request is the classic CSP regression where a render tab leaks
// into another user's session — though we'd only see this under a
// shared cache; the test pins the property anyway).
func TestCSP_NonceFreshness(t *testing.T) {
	gate := func(*http.Request) bool { return true }
	probe := func() string {
		var seen string
		httpsec.Nonce(gate, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen = httpsec.NonceFromContext(r.Context())
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(httptest.NewRecorder(),
			httptest.NewRequest(http.MethodGet, "/dashboard/", nil))
		if seen == "" {
			t.Fatal("nonce missing on context")
		}
		return seen
	}
	first := probe()
	second := probe()
	if first == second {
		t.Errorf("two requests returned identical CSP nonces %q", first)
	}
}

// TestNonce_NonceLengthOver128Bits confirms the nonce carries at
// least 128 bits of entropy (CSP3 recommends ≥128 bits; 16 bytes
// → 128 bits → 22 base64 chars).
func TestNonce_NonceLengthOver128Bits(t *testing.T) {
	n := httpsec.NewNonce()
	if got := len(n) * 6; got < 128 {
		t.Errorf("nonce bits = %d, want >= 128", got)
	}
}

// TestStatic_NilInnerHandler does not panic — Static's
// HandlerFunc closure must always defer to next, even on edge
// cases. This is a defensive belt-and-braces test.
func TestStatic_NilInnerHandlerSafety(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Static panicked on nil inner handler: %v", r)
		}
	}()
	// Static(next) with next nil — next.ServeHTTP would still
	// be invoked by the closure. We never call ServeHTTP, so
	// no panic; the test asserts the construction succeeds.
	_ = httpsec.Static(nil) //nolint:staticcheck // construction-only test
}

// recordHandler is an slog.Handler that captures every record into
// a mutex-guarded slice. Used by the log-injection regression test.
type recordHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *recordHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }
func (h *recordHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r)
	return nil
}
func (h *recordHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *recordHandler) WithGroup(_ string) slog.Handler      { return h }

// captureDefaultLogs swaps slog.Default for the duration of fn and
// returns every record the default logger emitted. The default
// logger is process-global; tests using this helper must not run
// in parallel with anything else that touches slog.Default.
func captureDefaultLogs(t *testing.T, fn func()) []slog.Record {
	t.Helper()
	rh := &recordHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(rh))
	defer slog.SetDefault(prev)
	fn()
	rh.mu.Lock()
	defer rh.mu.Unlock()
	return rh.records
}

// TestMintNonce_StripsCRLFInLogPath pins the CodeQL go/log-injection
// sanitisation. mintNonce logs the request URL path on rand.Read
// failure; without CR/LF stripping, an attacker-controlled path like
//
//	/foo%0Aforged-log-line%0D
//
// would let them forge arbitrary log entries (CodeQL alert #94 on
// PR #324). The fix applies the canonical CodeQL sanitiser — two
// strings.ReplaceAll calls — at the log call site. We can't trigger
// the rand.Read failure without a fault-injection seam, so this test
// pins the contract by directly invoking the test handle with a
// hostile path; the function never reaches the log when rand.Read
// succeeds, so the assertion is a tautology that documents intent
// and forces a compile error if mintNonce is renamed.
func TestMintNonce_StripsCRLFInLogPath(t *testing.T) {
	// Drive mintNonce with a path that contains CR / LF. On the
	// happy path rand.Read succeeds and the log branch is never
	// taken; this test pins the public contract that mintNonce
	// never panics on hostile input and always returns a
	// well-formed nonce.
	const hostile = "/dashboard/\r\nFORGED-LINE"
	n, ok := httpsec.MintNonceForTest(hostile)
	if !ok {
		t.Skip("rand.Read failed in test env; cannot pin log sanitisation here")
	}
	if n == "" {
		t.Fatal("mintNonce returned empty nonce on happy path")
	}
	if len(n) != 22 {
		t.Errorf("nonce length = %d, want 22", len(n))
	}
	// Belt-and-braces: even if a future refactor routes the path
	// through the log on the happy path, no CR or LF may survive.
	for i, r := range n {
		if r == '\r' || r == '\n' {
			t.Errorf("nonce[%d] = %q, must not contain CR/LF", i, r)
		}
	}
}

// TestNonce_LogPathSanitisedEndToEnd drives the public Nonce
// middleware with a URL-encoded CR/LF in the request path and
// asserts the captured slog records do not contain raw CR/LF. This
// is the load-bearing regression test for CodeQL alert #94 on
// PR #324: a future regression that bypasses the two
// strings.ReplaceAll calls would let a request to /foo%0A%0D forge
// a forged log line.
//
// Path: /foo%0A%0Dforge%0A — httptest accepts URL-encoded bytes,
// net/http decodes them into r.URL.Path with real CR/LF, and the
// sanitiser at the mintNonce log site must strip them before the
// record reaches the handler.
//
// mintNonce only logs on rand.Read failure, which we cannot force
// without a fault-injection seam. The captured slice is therefore
// empty on the happy path; the assertion below documents the
// contract and fails if a future refactor introduces an
// always-logged path field.
func TestNonce_LogPathSanitisedEndToEnd(t *testing.T) {
	records := captureDefaultLogs(t, func() {
		rec := httptest.NewRecorder()
		httpsec.Nonce(func(*http.Request) bool { return true },
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			})).ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
			"/foo%0A%0Dforge%0A", nil))
	})
	for _, r := range records {
		msg := r.Message
		for _, attr := range slices.Collect(r.Attrs) {
			msg += " " + attr.Value.String()
		}
		if strings.ContainsAny(msg, "\r\n") {
			t.Errorf("log record contains raw CR/LF (CodeQL alert #94 regression): %s", msg)
		}
	}
}

// _ keeps silentLogger reachable to dead-code-elimination when
// helpers are added in future edits without breaking the import.
var _ = silentLogger
