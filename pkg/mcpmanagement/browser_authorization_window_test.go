// adr: 20261117173000
package mcpmanagement

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Move the pre-consent deadlines back instead of sleeping through MFA or
// review. The browser binding, native exchange and consent still use HTTP.
func elapseBrowserAuthorization(t *testing.T, r *remoteServer, flow browserFlow, elapsed time.Duration) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	request := r.requests[flow.requestID]
	if request == nil {
		t.Fatal("browser authorization disappeared before the simulated delay")
	}
	request.Expires = request.Expires.Add(-elapsed)
	if grant := r.grants[request.GrantID]; grant != nil {
		grant.expires = grant.expires.Add(-elapsed)
	}
}

func browserReviewCSRF(t *testing.T, f *remoteFixture, flow browserFlow) string {
	t.Helper()
	response, err := flow.client.Get(f.server.URL + "/mcp/oauth/continue")
	if err != nil {
		t.Fatal(err)
	}
	page := readPage(t, response)
	if response.StatusCode != http.StatusOK || !strings.Contains(page, "Connect this account") {
		t.Fatalf("native return after MFA = %d: %s", response.StatusCode, page)
	}
	for _, field := range hiddenInput.FindAllStringSubmatch(page, -1) {
		if field[1] == "csrf" {
			return field[2]
		}
	}
	t.Fatal("account review has no CSRF token")
	return ""
}

func TestMCPBrowserAuthorizationSurvivesLongMFAAndConsent(t *testing.T) {
	store, _, _ := testStateStore(t)
	f := newRemoteFixtureWithOptions(t, false, HTTPOptions{Onboarding: "browser", Hosted: true, StateStore: store})
	service, err := NewHTTPService(f.opts)
	if err != nil {
		t.Fatal(err)
	}
	f.setHandler(service.Handler())
	flow := f.begin(scopeRead)
	if c := flow.cookie; c == nil || c.MaxAge != 3600 || !c.Secure || !c.HttpOnly || c.Path != "/" || c.SameSite != http.SameSiteLaxMode {
		t.Fatal("the browser binding must remain secure and cover the one-hour setup window")
	}
	start := f.submitConsent(flow, url.Values{"request_id": {flow.requestID}, "csrf": {flow.csrf}, "decision": {"start-login"}}, f.server.URL)
	_ = start.Body.Close()
	if start.StatusCode != http.StatusSeeOther {
		t.Fatalf("native sign-in = %d", start.StatusCode)
	}
	elapseBrowserAuthorization(t, service.remote, flow, 20*time.Minute)
	csrf := browserReviewCSRF(t, f, flow)
	if csrf == flow.csrf {
		t.Fatal("native return did not rotate the CSRF token")
	}
	elapseBrowserAuthorization(t, service.remote, flow, 20*time.Minute)
	// Spending longer on the form must not make an old or forged token valid.
	for _, invalid := range []string{flow.csrf, "forged"} {
		response := f.consent(flow, "approve", "", f.server.URL, invalid, false, "alpha")
		_ = response.Body.Close()
		if response.StatusCode != http.StatusForbidden {
			t.Fatalf("stale/forged consent after the delay = %d", response.StatusCode)
		}
	}
	approved := f.consent(flow, "approve", "", f.server.URL, csrf, false, "alpha")
	_ = approved.Body.Close()
	callback, err := url.Parse(approved.Header.Get("Location"))
	if err != nil || approved.StatusCode != http.StatusSeeOther || callback.Query().Get("state") != "client-state" || callback.Query().Get("iss") != f.resource {
		t.Fatalf("consent after 40 minutes = %d", approved.StatusCode)
	}
	code := callback.Query().Get("code")
	service.remote.mu.Lock()
	issued := service.remote.codes[tokenHash(code)]
	shortCode := issued != nil && time.Until(issued.Expires) > 55*time.Second && time.Until(issued.Expires) <= time.Minute
	service.remote.mu.Unlock()
	if !shortCode {
		t.Fatal("the final client authorization code must still expire after one minute")
	}
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {testVerifier}, "resource": {f.resource}, "client_id": {f.clientID}, "redirect_uri": {testRedirect}}
	response := f.request(http.MethodPost, "/mcp/oauth/token", strings.NewReader(form.Encode()), "application/x-www-form-urlencoded", "")
	var tokens tokenResponse
	decodeResponse(t, response, &tokens)
	if response.StatusCode != http.StatusOK || tokens.AccessToken == "" {
		t.Fatalf("token exchange after the delayed setup = %d", response.StatusCode)
	}
	client, ctx := f.connect(tokens, "2026-07-28")
	defer client.Close()
	result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "gregale_list_apps", Arguments: map[string]any{"limit": 1}})
	if err != nil || result.IsError {
		t.Fatalf("MCP read after delayed setup: %v", err)
	}
	replay := f.request(http.MethodPost, "/mcp/oauth/token", strings.NewReader(form.Encode()), "application/x-www-form-urlencoded", "")
	_ = replay.Body.Close()
	if replay.StatusCode != http.StatusBadRequest {
		t.Fatal("the client authorization code was reusable")
	}
}

func TestMCPBrowserAuthorizationStillExpiresAfterItsSetupWindow(t *testing.T) {
	for _, stage := range []string{"before sign-in", "MFA", "consent"} {
		t.Run(stage, func(t *testing.T) {
			store, _, _ := testStateStore(t)
			f := newRemoteFixtureWithOptions(t, false, HTTPOptions{Onboarding: "browser", Hosted: true, StateStore: store})
			service, err := NewHTTPService(f.opts)
			if err != nil {
				t.Fatal(err)
			}
			f.setHandler(service.Handler())
			flow := f.begin(scopeRead)
			csrf := flow.csrf
			if stage != "before sign-in" {
				start := f.submitConsent(flow, url.Values{"request_id": {flow.requestID}, "csrf": {csrf}, "decision": {"start-login"}}, f.server.URL)
				_ = start.Body.Close()
			}
			if stage == "consent" {
				csrf = browserReviewCSRF(t, f, flow)
			}
			elapseBrowserAuthorization(t, service.remote, flow, 61*time.Minute)
			response := f.consent(flow, "approve", "", f.server.URL, csrf, false, "alpha")
			_ = response.Body.Close()
			if response.StatusCode != http.StatusForbidden || response.Header.Get("Location") != "" {
				t.Fatalf("expired %s transaction = %d", stage, response.StatusCode)
			}
			service.remote.mu.Lock()
			codes := len(service.remote.codes)
			_, survives := service.remote.requests[flow.requestID]
			service.remote.mu.Unlock()
			if codes != 0 || survives {
				t.Fatal("expired browser authorization retained or issued a client code")
			}
		})
	}
}
