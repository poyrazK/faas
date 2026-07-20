package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestACMEMux_RedirectsNonACMERequests — :80 traffic that isn't an ACME
// challenge must 308 to https://<host><uri>. Spec §4.1 "redirect + ACME
// HTTP-01". 308 (not 301) preserves the original method so POST/PUT/etc.
// survive the upgrade.
func TestACMEMux_RedirectsNonACMERequests(t *testing.T) {
	called := false
	challenge := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	})
	mux := NewACMEMux(challenge)

	for _, tc := range []struct {
		name     string
		method   string
		path     string
		host     string
		wantURL  string
		wantCode int
	}{
		{"root-308", http.MethodGet, "/", "jane-api.apps.dom", "https://jane-api.apps.dom/", http.StatusPermanentRedirect},
		{"deep-path-308", http.MethodGet, "/api/v1/things?x=1", "jane-api.apps.dom", "https://jane-api.apps.dom/api/v1/things?x=1", http.StatusPermanentRedirect},
		{"custom-domain-308", http.MethodGet, "/", "shop.example.com", "https://shop.example.com/", http.StatusPermanentRedirect},
		{"post-308", http.MethodPost, "/webhook", "shop.example.com", "https://shop.example.com/webhook", http.StatusPermanentRedirect},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called = false
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Host = tc.host
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != tc.wantCode {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantCode)
			}
			got := rec.Header().Get("Location")
			if got != tc.wantURL {
				t.Errorf("Location = %q, want %q", got, tc.wantURL)
			}
			if called {
				t.Error("ACME handler should not have been invoked for non-challenge paths")
			}
		})
	}
}

// TestACMEMux_DispatchesACMEPath — the well-known challenge prefix must
// reach the challenge handler, not the redirect. CertMagic's solver polls
// /.well-known/acme-challenge/<token> on :80 during validation; a 308 here
// would break the cert-mint path.
func TestACMEMux_DispatchesACMEPath(t *testing.T) {
	called := false
	challenge := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		_, _ = io.WriteString(w, "challenge-response")
	})
	mux := NewACMEMux(challenge)

	req := httptest.NewRequest(http.MethodGet, "/.well-known/acme-challenge/abc123", nil)
	req.Host = "jane-api.apps.dom"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if !called {
		t.Fatal("ACME challenge handler was not invoked")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (challenge response served)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "challenge-response") {
		t.Errorf("body = %q, want challenge response", rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "" {
		t.Errorf("ACME path must not redirect; got Location=%q", loc)
	}
}

// TestACMEMux_NilChallengeHandler — graceful degradation: a nil challenge
// handler still constructs (the mux is mountable on :80) and 404s ACME
// requests rather than panicking.
func TestACMEMux_NilChallengeHandler(t *testing.T) {
	mux := NewACMEMux(nil)
	req := httptest.NewRequest(http.MethodGet, "/.well-known/acme-challenge/abc", nil)
	req.Host = "jane-api.apps.dom"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (no challenge handler)", rec.Code)
	}
}

// TestACMEMux_RejectsMissingHost — the redirect path needs a host header;
// an empty host would produce "Location: https:///foo" which is malformed.
// 400 is the spec-blessed response to a missing Host header.
func TestACMEMux_RejectsMissingHost(t *testing.T) {
	mux := NewACMEMux(nil)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "" // httptest's default; explicit for clarity
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (missing host)", rec.Code)
	}
}

// TestAllowlistToDecisionFunc — the adapter from OnDemandAllowlist (bool)
// to certmagic's DecisionFunc (error) is the bridge between pkg/gateway and
// certmagic. Verify it both denies (false → non-nil error) and allows (true → nil).
func TestAllowlistToDecisionFunc(t *testing.T) {
	deny := func(string) bool { return false }
	df := allowlistToDecisionFunc(deny, nil)
	if err := df(nil, "attacker.example.com"); err == nil {
		t.Error("deny allowlist should produce non-nil error")
	}

	allow := func(string) bool { return true }
	df = allowlistToDecisionFunc(allow, nil)
	if err := df(nil, "shop.example.com"); err != nil {
		t.Errorf("allow allowlist should produce nil error, got %v", err)
	}
}

// TestAllowlistToDecisionFunc_NilAllowlist — a misconfigured certmagic
// config (allowlist not wired) must refuse to mint anything. This is the
// defense-in-depth for Validate()'s OnDemandHTTP01Allowlist == nil check.
func TestAllowlistToDecisionFunc_NilAllowlist(t *testing.T) {
	df := allowlistToDecisionFunc(nil, nil)
	if err := df(nil, "anything.example.com"); err == nil {
		t.Error("nil allowlist must produce non-nil error")
	}
}