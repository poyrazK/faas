// adr: 079
// Per-app public_auth tests (issue #477 / ADR-079).
//
// Pins the public-auth branch behaviour end-to-end through
// ServeHTTP for every mode (open, bearer, basic), the
// regression pins (mode=” or mode='open' doesn't fire the
// branch; bearer mode re-uses the require_authn chain), the
// cache hit/miss semantics, and the audit-kind surface.
// Mirrors pkg/gateway/require_authn_test.go so an operator
// reading the two files side-by-side sees parallel shapes.
//
// fakePublicAuthUnsealer is the test seam for the basic-auth
// branch — same PublicAuthUnsealer interface the production
// adapter (cmd/gatewayd-internal/public_auth_unsealer.go)
// satisfies. Each test seeds the credentials it wants and
// the assertions read them back via the atomic counter.
package gateway

import (
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// fakePublicAuthUnsealer is the test seam for the basic-auth
// branch. Each test seeds (user, pass) + a sealed-blob-key
// map so it can verify "the right blob unsealed to the right
// creds". A nil return error = ok=true (the seal-then-open
// round-trip succeeded).
type fakePublicAuthUnsealer struct {
	// calls counts every UnsealBasicAuth invocation. Tests
	// pin "exactly one unseal per cache miss" via this
	// counter.
	calls atomic.Int32
	// sealedToCreds maps the sealed-blob bytes the
	// application presents to the {user, pass} pair the
	// unsealer returns. Empty + no entry → ok=false (the
	// "blob tampered" path).
	sealedToCreds map[string][2]string
	// failBlob is the sealed-blob byte string that triggers
	// a hard failure regardless of sealedToCreds. Tests
	// that exercise the tampered-blob path set this.
	failBlob string
}

func (f *fakePublicAuthUnsealer) UnsealBasicAuth(_ context.Context, sealed []byte) (string, string, error) {
	f.calls.Add(1)
	if string(sealed) == f.failBlob {
		return "", "", errPublicAuthUnsealerTest
	}
	creds, ok := f.sealedToCreds[string(sealed)]
	if !ok {
		return "", "", errPublicAuthUnsealerTest
	}
	return creds[0], creds[1], nil
}

// errPublicAuthUnsealerTest is the local sentinel for the
// fakePublicAuthUnsealer error path. Tests don't read the
// specific error string; the handler collapses every error
// to a 401.
var errPublicAuthUnsealerTest = publicAuthTestErr("fakePublicAuthUnsealer: forced failure")

type publicAuthTestErr string

func (e publicAuthTestErr) Error() string { return string(e) }

// newPublicAuthTestHandler wires a Handler with a single
// routed app on the supplied host, gated by the supplied
// PublicAuth mode (open|bearer|basic). The fake authenticator
// + unsealer + auditor are returned so each test can seed
// the response it wants.
func newPublicAuthTestHandler(t *testing.T, mode string, accountID string) (*Handler, *fakeBackend, *fakeRequireAuthnAuthn, *fakePublicAuthUnsealer, *fakeRequireAuthnAudit) {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("hello from app"))
	}))
	t.Cleanup(upstream.Close)

	b := &fakeBackend{
		app: App{
			ID:        "app-1",
			AccountID: accountID,
			Plan:      api.PlanPro,
			PublicAuth: PublicAuthConfig{
				Mode:        mode,
				BasicSealed: []byte("sealed-blob-A"),
			},
		},
		host:                   "jane-api.apps.dom",
		upstream:               upstream.Listener.Addr().String(),
		preservePublicAuthMode: true,
	}
	b.setLegacyHot()

	authn := &fakeRequireAuthnAuthn{accountID: accountID, keyID: "key-1"}
	unsealer := &fakePublicAuthUnsealer{
		sealedToCreds: map[string][2]string{
			"sealed-blob-A": {"alice", "s3cret"},
		},
	}
	audit := newFakeRequireAuthnAudit()

	h := NewHandlerWith(b, NewMetrics(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	h.WithRequireAuthn(authn, audit)
	h.WithPublicAuth(NewPublicAuthCache(), unsealer)
	return h, b, authn, unsealer, audit
}

// publicAuthReqFor builds a request against the test
// handler's host with the supplied Authorization header
// (empty → no header at all).
func publicAuthReqFor(t *testing.T, authHeader string) *http.Request {
	t.Helper()
	req := httptest.NewRequest("GET", "http://jane-api.apps.dom/", nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	return req
}

// TestPublicAuth_OpenMode_AllowsAnonymous pins the
// regression: mode='open' is the pre-#477 default and must
// pass through anonymous traffic.
func TestPublicAuth_OpenMode_AllowsAnonymous(t *testing.T) {
	t.Parallel()
	h, _, _, _, _ := newPublicAuthTestHandler(t, "open", "acct-1")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, publicAuthReqFor(t, ""))
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
}

// TestPublicAuth_InvalidMode_FailsClosed pins the security boundary: empty
// and unknown modes return a stable 503 before auth, wake admission, or the
// upstream proxy. The wire response and audit payload do not echo the bad
// mode value.
func TestPublicAuth_InvalidMode_FailsClosed(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		mode   string
		reason string
	}{
		{name: "empty", mode: "", reason: publicAuthConfigReasonEmpty},
		{name: "unknown", mode: "not-a-mode", reason: publicAuthConfigReasonUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, b, authn, _, audit := newPublicAuthTestHandler(t, tc.mode, "acct-1")
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, publicAuthReqFor(t, ""))

			if rr.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503", rr.Code)
			}
			if !strings.Contains(rr.Body.String(), api.CodePublicAuthConfigInvalid) {
				t.Fatalf("body = %q, want code %q", rr.Body.String(), api.CodePublicAuthConfigInvalid)
			}
			if tc.mode != "" && strings.Contains(rr.Body.String(), tc.mode) {
				t.Fatalf("body echoed invalid mode %q: %q", tc.mode, rr.Body.String())
			}
			if got := atomic.LoadInt32(b.Admits()); got != 0 {
				t.Errorf("wake admits = %d, want 0", got)
			}
			if got := authn.calls.Load(); got != 0 {
				t.Errorf("auth calls = %d, want 0", got)
			}
			if got := audit.counts["instances.public_auth_config_invalid"]; got != 1 {
				t.Errorf("invalid-config audit count = %d, want 1", got)
			}
			if got := audit.lastData["reason"]; got != tc.reason {
				t.Errorf("audit reason = %v, want %q", got, tc.reason)
			}
			if got := testutil.ToFloat64(h.metrics.publicAuthConfigErrors.WithLabelValues(tc.reason)); got != 1 {
				t.Errorf("metric reason=%q = %v, want 1", tc.reason, got)
			}
		})
	}
}

// TestPublicAuth_BearerMissing_401 pins the bearer-mode
// missing-header path: 401 + WWW-Authenticate +
// instances.public_auth_missing audit row.
func TestPublicAuth_BearerMissing_401(t *testing.T) {
	t.Parallel()
	h, _, _, _, audit := newPublicAuthTestHandler(t, "bearer", "acct-1")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, publicAuthReqFor(t, ""))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
	if got := rr.Header().Get("WWW-Authenticate"); got != `Bearer realm="apps"` {
		t.Errorf("WWW-Authenticate = %q, want %q", got, `Bearer realm="apps"`)
	}
	if audit.counts["instances.public_auth_missing"] != 1 {
		t.Errorf("missing audit count = %d, want 1", audit.counts["instances.public_auth_missing"])
	}
}

// TestPublicAuth_BearerValid_Allows pins the bearer-mode
// success path: valid key on the owning account → 200, the
// authn chain fires exactly once.
func TestPublicAuth_BearerValid_Allows(t *testing.T) {
	t.Parallel()
	h, _, authn, _, _ := newPublicAuthTestHandler(t, "bearer", "acct-1")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, publicAuthReqFor(t, "Bearer "+validKeyFormat("000000000000000000000000000000000000000000000001")))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if got := authn.calls.Load(); got != 1 {
		t.Errorf("AuthenticateKey calls = %d, want 1", got)
	}
}

// TestPublicAuth_BearerCrossAccount_403 pins the bearer-mode
// cross-account path: valid key but on the wrong account →
// 403 + instances.public_auth_scope audit row with
// caller_account_id in the payload.
func TestPublicAuth_BearerCrossAccount_403(t *testing.T) {
	t.Parallel()
	h, _, authn, _, audit := newPublicAuthTestHandler(t, "bearer", "acct-1")
	authn.accountID = "acct-other" // cross-account: app owns acct-1
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, publicAuthReqFor(t, "Bearer "+validKeyFormat("000000000000000000000000000000000000000000000001")))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
	if audit.counts["instances.public_auth_scope"] != 1 {
		t.Errorf("scope audit count = %d, want 1", audit.counts["instances.public_auth_scope"])
	}
	if audit.lastData["mode"] != "bearer" {
		t.Errorf("scope payload mode = %v, want bearer", audit.lastData["mode"])
	}
}

// TestPublicAuth_BearerExpired_401 pins the bearer-mode
// expired-key path: 401 + instances.public_auth_invalid
// with reason='expired'.
func TestPublicAuth_BearerExpired_401(t *testing.T) {
	t.Parallel()
	h, _, authn, _, audit := newPublicAuthTestHandler(t, "bearer", "acct-1")
	authn.err = ErrAPIKeyExpired
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, publicAuthReqFor(t, "Bearer "+validKeyFormat("000000000000000000000000000000000000000000000001")))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
	if audit.counts["instances.public_auth_invalid"] != 1 {
		t.Errorf("invalid audit count = %d, want 1", audit.counts["instances.public_auth_invalid"])
	}
	if audit.lastData["reason"] != "expired" {
		t.Errorf("reason = %v, want expired", audit.lastData["reason"])
	}
}

// TestPublicAuth_BasicMissing_401 pins the basic-mode
// missing-header path: 401 + WWW-Authenticate: Basic +
// instances.public_auth_missing audit row.
func TestPublicAuth_BasicMissing_401(t *testing.T) {
	t.Parallel()
	h, _, _, _, audit := newPublicAuthTestHandler(t, "basic", "acct-1")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, publicAuthReqFor(t, ""))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
	if got := rr.Header().Get("WWW-Authenticate"); got != `Basic realm="apps"` {
		t.Errorf("WWW-Authenticate = %q, want %q", got, `Basic realm="apps"`)
	}
	if audit.counts["instances.public_auth_missing"] != 1 {
		t.Errorf("missing audit count = %d, want 1", audit.counts["instances.public_auth_missing"])
	}
}

// TestPublicAuth_BasicValid_Allows pins the basic-mode
// success path: valid creds → 200, the unsealer fires
// exactly once (second call is a cache hit).
func TestPublicAuth_BasicValid_Allows(t *testing.T) {
	t.Parallel()
	h, _, _, unsealer, _ := newPublicAuthTestHandler(t, "basic", "acct-1")
	creds := base64.StdEncoding.EncodeToString([]byte("alice:s3cret"))
	for i := 0; i < 3; i++ {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, publicAuthReqFor(t, "Basic "+creds))
		if rr.Code != http.StatusOK {
			t.Fatalf("call %d: status = %d, want 200", i, rr.Code)
		}
	}
	if got := unsealer.calls.Load(); got != 1 {
		t.Errorf("UnsealBasicAuth calls = %d, want 1 (cache hit on calls 2 + 3)", got)
	}
}

// TestPublicAuth_BasicWrongPassword_401 pins the
// constant-time-compare path: wrong password → 401 +
// instances.public_auth_invalid with reason='wrong_credentials'.
func TestPublicAuth_BasicWrongPassword_401(t *testing.T) {
	t.Parallel()
	h, _, _, _, audit := newPublicAuthTestHandler(t, "basic", "acct-1")
	creds := base64.StdEncoding.EncodeToString([]byte("alice:wrong"))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, publicAuthReqFor(t, "Basic "+creds))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
	if audit.counts["instances.public_auth_invalid"] != 1 {
		t.Errorf("invalid audit count = %d, want 1", audit.counts["instances.public_auth_invalid"])
	}
	if audit.lastData["reason"] != "wrong_credentials" {
		t.Errorf("reason = %v, want wrong_credentials", audit.lastData["reason"])
	}
}

// TestPublicAuth_BasicUnsealFailure_401 pins the
// "blob tampered / no creds configured" path: an unseal
// failure surfaces as 401 (NOT 500) so a brute-forcer
// can't distinguish it from a wrong-password attempt.
func TestPublicAuth_BasicUnsealFailure_401(t *testing.T) {
	t.Parallel()
	h, b, _, unsealer, _ := newPublicAuthTestHandler(t, "basic", "acct-1")
	b.app.PublicAuth.BasicSealed = []byte("tampered")
	unsealer.failBlob = "tampered"
	creds := base64.StdEncoding.EncodeToString([]byte("alice:s3cret"))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, publicAuthReqFor(t, "Basic "+creds))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
	if got := rr.Header().Get("WWW-Authenticate"); got != `Basic realm="apps"` {
		t.Errorf("WWW-Authenticate = %q, want %q (force the client to retry with creds)", got, `Basic realm="apps"`)
	}
}

// TestBasicCredsFromHeader_Pin pins the RFC 7617 §2 parser
// independently of enforcePublicAuth — every malformed
// input collapses to ok=false.
func TestBasicCredsFromHeader_Pin(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		header string
		wantU  string
		wantP  string
		wantOK bool
	}{
		{"empty", "", "", "", false},
		{"no_scheme", base64.StdEncoding.EncodeToString([]byte("a:b")), "", "", false},
		{"wrong_scheme", "Bearer " + base64.StdEncoding.EncodeToString([]byte("a:b")), "", "", false},
		{"empty_after_scheme", "Basic ", "", "", false},
		{"not_base64", "Basic !!notbase64!!", "", "", false},
		{"no_colon", "Basic " + base64.StdEncoding.EncodeToString([]byte("nocolon")), "", "", false},
		{"empty_user", "Basic " + base64.StdEncoding.EncodeToString([]byte(":secret")), "", "", false},
		{"empty_pass", "Basic " + base64.StdEncoding.EncodeToString([]byte("alice:")), "", "", false},
		{"happy", "Basic " + base64.StdEncoding.EncodeToString([]byte("alice:s3cret")), "alice", "s3cret", true},
		{"password_has_colon", "Basic " + base64.StdEncoding.EncodeToString([]byte("alice:a:b:c")), "alice", "a:b:c", true},
		{"case_insensitive_scheme", "basic " + base64.StdEncoding.EncodeToString([]byte("alice:s3cret")), "alice", "s3cret", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u, p, ok := basicCredsFromHeader(tc.header)
			if ok != tc.wantOK || u != tc.wantU || p != tc.wantP {
				t.Errorf("got (%q,%q,%v) want (%q,%q,%v)", u, p, ok, tc.wantU, tc.wantP, tc.wantOK)
			}
		})
	}
}

// TestPublicAuth_IPAllowlist (ADR-118) pins the gateway-side
// applyIngressIPAllowlist gate end-to-end through ServeHTTP. The
// four cases cover the load-bearing branches:
//
//  1. Mode=ip_allowlist + client IP in list → 200 pass-through
//     (the matched metric emits outcome="match" so the §12
//     dashboard surfaces the allow side too, not only blocked).
//  2. Mode=ip_allowlist + client IP NOT in list → 403 with
//     edge_rule.ingress_ip_blocked audit + outcome=blocked.
//  3. Mode=ip_allowlist + missing/duplicated XFF → 403 with
//     edge_rule.ingress_ip_forged audit (defense-in-depth).
//  4. Mode=ip_allowlist + EMPTY allowlist → 500 operator_error
//     (not 403, not silent pass-through) — the loud posture.
//
// Trust chain is identical to applyEdgeRuleIP:
// clientIPFromTrustedXFF (single XFF entry). Tests inject the
// trusted XFF directly because gatewayd-public is a separate
// daemon; in production the XFF overwrite at pkg/gateway/
// internal_proxy.go:286-289 makes the test's header shape
// identical to production.
func TestPublicAuth_IPAllowlist(t *testing.T) {
	// Build the handler once; each subtest mutates the
	// fakeBackend's PublicAuth shape and re-runs ServeHTTP
	// through the same recorder. The test handler wires
	// the same fakes as the existing public_auth tests
	// (authn, unsealer, audit) so the gate's surface
	// (200/403/500) is observable through the response
	// recorder without spinning up a real upstream.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("hello from app"))
	}))
	t.Cleanup(upstream.Close)

	b := &fakeBackend{
		app: App{
			ID:        "app-1",
			AccountID: "acct-1",
			Plan:      api.PlanPro,
			PublicAuth: PublicAuthConfig{
				Mode: publicAuthModeIPAllowlist,
				IPAllowlist: []netip.Prefix{
					netip.MustParsePrefix("10.0.0.0/8"),
					netip.MustParsePrefix("192.0.2.0/24"),
				},
			},
		},
		host:     "jane-api.apps.dom",
		upstream: upstream.Listener.Addr().String(),
	}
	b.setLegacyHot()

	audit := newFakeRequireAuthnAudit()
	h := NewHandlerWith(b, NewMetrics(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	h.WithRequireAuthn(&fakeRequireAuthnAuthn{accountID: "acct-1", keyID: "key-1"}, audit)
	h.WithPublicAuth(NewPublicAuthCache(), &fakePublicAuthUnsealer{})

	t.Run("xff_in_allowlist_returns_200", func(t *testing.T) {
		req := publicAuthReqFor(t, "")
		req.Header.Set("X-Forwarded-For", "10.5.5.5")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("xff_in_allowlist: code=%d body=%s; want 200",
				rec.Code, rec.Body.String())
		}
	})
	t.Run("xff_not_in_allowlist_returns_403", func(t *testing.T) {
		req := publicAuthReqFor(t, "")
		req.Header.Set("X-Forwarded-For", "203.0.113.5") // TEST-NET-3
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("xff_not_in_allowlist: code=%d body=%s; want 403",
				rec.Code, rec.Body.String())
		}
	})
	t.Run("xff_missing_returns_403_forged", func(t *testing.T) {
		req := publicAuthReqFor(t, "")
		// No X-Forwarded-For — defense-in-depth must fail closed.
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("xff_missing: code=%d body=%s; want 403",
				rec.Code, rec.Body.String())
		}
	})
	t.Run("empty_allowlist_returns_500", func(t *testing.T) {
		// Mutate the fakeBackend to expose the misconfig
		// posture: mode=ip_allowlist with no CIDRs. This
		// is the loud-posture arm — 500, not 403, not
		// pass-through. A reader of the audit row sees
		// "app is misconfigured: ip_allowlist mode
		// requires at least one CIDR" and knows to fix
		// the row.
		b.app.PublicAuth.IPAllowlist = nil
		defer func() {
			b.app.PublicAuth.IPAllowlist = []netip.Prefix{
				netip.MustParsePrefix("10.0.0.0/8"),
				netip.MustParsePrefix("192.0.2.0/24"),
			}
		}()
		req := publicAuthReqFor(t, "")
		req.Header.Set("X-Forwarded-For", "10.5.5.5")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("empty_allowlist: code=%d body=%s; want 500",
				rec.Code, rec.Body.String())
		}
	})
}
