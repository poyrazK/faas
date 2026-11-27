package gateway

import (
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// previewCredentialHandler routes one app gated by mode, whose upstream
// answers with the Authorization header it received.
func previewCredentialHandler(t *testing.T, mode string, preview bool) *Handler {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(r.Header.Get("Authorization")))
	}))
	t.Cleanup(upstream.Close)
	b := &fakeBackend{
		app: App{
			ID: "app-1", AccountID: "acct-1", Plan: api.PlanPro, IsPreview: preview,
			PublicAuth: PublicAuthConfig{Mode: mode, BasicSealed: []byte("sealed-blob-A")},
		},
		host:                   "jane-api.apps.dom",
		upstream:               upstream.Listener.Addr().String(),
		preservePublicAuthMode: true,
	}
	b.setLegacyHot()
	h := NewHandlerWith(b, NewMetrics(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	h.WithRequireAuthn(&fakeRequireAuthnAuthn{accountID: "acct-1", keyID: "key-1"}, newFakeRequireAuthnAudit())
	h.WithPublicAuth(NewPublicAuthCache(), &fakePublicAuthUnsealer{
		sealedToCreds: map[string][2]string{"sealed-blob-A": {"alice", "s3cret"}},
	})
	return h
}

// A preview keeps its production app's password and API-key gates, and runs
// a pull request's unreviewed code. The gateway handed the verified
// Authorization header on, so the PR's code could read the production
// password or the caller's Gregale API key (ADR-20261102100001).
func TestPreviewDoesNotReceiveGateCredential(t *testing.T) {
	basic := "Basic " + base64.StdEncoding.EncodeToString([]byte("alice:s3cret"))
	bearer := "Bearer " + validKeyFormat("000000000000000000000000000000000000000000000001")
	for _, tc := range []struct {
		name, mode, header string
		preview            bool
		want               string
	}{
		{name: "preview basic", mode: publicAuthModeBasic, header: basic, preview: true, want: ""},
		{name: "preview bearer", mode: publicAuthModeBearer, header: bearer, preview: true, want: ""},
		{name: "production basic", mode: publicAuthModeBasic, header: basic, want: basic},
		// prod 2026-11-20 (R12-1): a production app received the caller's
		// account API key (ADR-20261120060000).
		{name: "production bearer", mode: publicAuthModeBearer, header: bearer, want: ""},
		{name: "open preview keeps the app's own scheme", mode: publicAuthModeOpen, header: "Token app-owned", preview: true, want: "Token app-owned"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := previewCredentialHandler(t, tc.mode, tc.preview)
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/", nil)
			req.Header.Set("Authorization", tc.header)
			h.ServeHTTP(rr, req)
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body)
			}
			if got := rr.Body.String(); got != tc.want {
				t.Fatalf("app received Authorization %q, want %q", got, tc.want)
			}
		})
	}
}

// The withheld credential still makes the request credentialed for the
// shared cache, as the header did.
func TestWithheldPreviewCredentialStillBypassesSharedCache(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/", nil)
	req.Header.Set("Authorization", "Basic YWxpY2U6czNjcmV0")
	withholdPreviewGateCredential(req, App{IsPreview: true, PublicAuth: PublicAuthConfig{Mode: publicAuthModeBasic}})
	if req.Header.Get("Authorization") != "" {
		t.Fatal("the gate credential was not removed")
	}
	if !hasCacheCredentials(req) {
		t.Fatal("a request whose credential was withheld must still bypass the shared cache")
	}
}

// prod 2026-11-20 (R12-1): require_authn on a production app verified the
// caller's account API key and then handed it to the app. The key is
// withheld; an app's own scheme and a production basic-auth password still
// reach the app (ADR-20261120060000).
func TestProductionAppDoesNotReceiveVerifiedAPIKey(t *testing.T) {
	key := "Bearer " + validKeyFormat("000000000000000000000000000000000000000000000002")
	for _, tc := range []struct {
		name   string
		app    App
		header string
		want   string
	}{
		{"require_authn key", App{RequireAuthn: true}, key, ""},
		{"ungated app's own token", App{}, "Bearer app-own-jwt", "Bearer app-own-jwt"},
		{"ungated app receives a key it reads itself", App{}, key, key},
		{"production basic password", App{PublicAuth: PublicAuthConfig{Mode: publicAuthModeBasic}}, "Basic YWxpY2U6czNjcmV0", "Basic YWxpY2U6czNjcmV0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/", nil)
			req.Header.Set("Authorization", tc.header)
			withholdPreviewGateCredential(req, tc.app)
			if got := req.Header.Get("Authorization"); got != tc.want {
				t.Fatalf("Authorization = %q, want %q", got, tc.want)
			}
			if tc.want == "" && !hasCacheCredentials(req) {
				t.Fatal("a withheld key must still bypass the shared cache")
			}
		})
	}
}
