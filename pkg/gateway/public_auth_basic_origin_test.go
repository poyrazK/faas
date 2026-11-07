// adr: 20261104221012
package gateway

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
)

// corsOnlyMatcher answers MatchCORS with rule and nothing else.
type corsOnlyMatcher struct {
	noOpEdgeRuleMatcher
	rule *EdgeRuleCORSResolved
}

func (m corsOnlyMatcher) MatchCORS(context.Context, string, string, string) *EdgeRuleCORSResolved {
	return m.rule
}

// A browser resends cached Basic credentials to the app's origin whatever page
// started the request. A cross-origin WebSocket handshake or state-changing
// request is refused; same-origin requests and non-browser clients (no Origin)
// are not.
func TestPublicAuthBasicRefusesCrossOriginBrowserRequests(t *testing.T) {
	t.Parallel()
	creds := "Basic " + base64.StdEncoding.EncodeToString([]byte("alice:s3cret"))
	for _, tc := range []struct {
		name    string
		method  string
		origin  []string
		ws      bool
		want    int
		refused bool
	}{
		{name: "cross-origin POST", method: http.MethodPost, origin: []string{"https://evil.example"}, want: http.StatusForbidden, refused: true},
		{name: "null origin POST", method: http.MethodPost, origin: []string{"null"}, want: http.StatusForbidden, refused: true},
		{name: "duplicate origins", method: http.MethodPost, origin: []string{"https://jane-api.apps.dom", "https://evil.example"}, want: http.StatusForbidden, refused: true},
		{name: "cross-origin WebSocket", method: http.MethodGet, origin: []string{"https://evil.example"}, ws: true, want: http.StatusForbidden, refused: true},
		{name: "cross-origin GET", method: http.MethodGet, origin: []string{"https://evil.example"}, want: http.StatusOK},
		{name: "same-origin POST", method: http.MethodPost, origin: []string{"https://jane-api.apps.dom"}, want: http.StatusOK},
		{name: "no Origin POST", method: http.MethodPost, want: http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _, _, _, audit := newPublicAuthTestHandler(t, "basic", "acct-1")
			req := httptest.NewRequest(tc.method, "http://jane-api.apps.dom/", nil)
			req.Header.Set("Authorization", creds)
			for _, o := range tc.origin {
				req.Header.Add("Origin", o)
			}
			if tc.ws {
				req.Header.Set("Connection", "Upgrade")
				req.Header.Set("Upgrade", "websocket")
			}
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if tc.refused {
				if rr.Code != tc.want {
					t.Fatalf("status = %d, want %d", rr.Code, tc.want)
				}
				if audit.counts["instances.public_auth_cross_origin"] != 1 {
					t.Fatalf("cross-origin audit count = %d, want 1", audit.counts["instances.public_auth_cross_origin"])
				}
				return
			}
			if audit.counts["instances.public_auth_cross_origin"] != 0 {
				t.Fatalf("request refused as cross-origin: status %d", rr.Code)
			}
		})
	}
}

// An origin the owner's CORS rule allows with credentials may use the app's
// Basic credentials; one it allows without credentials may not.
func TestCrossOriginBasicAuthHonoursCredentialedCORSRule(t *testing.T) {
	t.Parallel()
	app := App{ID: "app-1", AccountID: "acct-1"}
	req := httptest.NewRequest(http.MethodPost, "http://jane-api.apps.dom/api", nil)
	req.Header.Set("Origin", "https://front.example")
	for _, tc := range []struct {
		name string
		rule *EdgeRuleCORSResolved
		want bool
	}{
		{name: "credentialed rule", rule: &EdgeRuleCORSResolved{AccountID: "acct-1", AllowOrigins: []string{"https://front.example"}, AllowCredentials: true}, want: false},
		{name: "rule without credentials", rule: &EdgeRuleCORSResolved{AccountID: "acct-1", AllowOrigins: []string{"https://front.example"}}, want: true},
		{name: "wildcard rule", rule: &EdgeRuleCORSResolved{AccountID: "acct-1", AllowOrigins: []string{"*"}, AllowCredentials: true}, want: true},
		{name: "another account's rule", rule: &EdgeRuleCORSResolved{AccountID: "acct-2", AllowOrigins: []string{"https://front.example"}, AllowCredentials: true}, want: true},
		{name: "no rule", want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &Handler{edgeRules: corsOnlyMatcher{rule: tc.rule}}
			if got := h.crossOriginBasicAuthRequest(req, app); got != tc.want {
				t.Fatalf("crossOriginBasicAuthRequest = %v, want %v", got, tc.want)
			}
		})
	}
}
