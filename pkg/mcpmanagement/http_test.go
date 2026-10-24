// adr: 1618
package mcpmanagement

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"

	"github.com/onebox-faas/faas/pkg/api"
)

const testRedirect = "https://client.example/oauth/callback"
const testVerifier = "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNO"

type remoteFixture struct {
	t          *testing.T
	server     *httptest.Server
	client     *http.Client
	resource   string
	clientID   string
	mu         sync.Mutex
	accounts   map[string]string
	apiHook    func(http.ResponseWriter, *http.Request, string) bool
	opts       HTTPOptions
	setHandler func(http.Handler)
}

func newRemoteFixture(t *testing.T, writes bool) *remoteFixture {
	return newRemoteFixtureWithOptions(t, writes, HTTPOptions{Onboarding: "api-key"})
}

func newRemoteFixtureWithOptions(t *testing.T, writes bool, options HTTPOptions) *remoteFixture {
	t.Helper()
	f := &remoteFixture{t: t, accounts: map[string]string{"fp_test_alice_remote_key": "alice", "fp_test_bob_remote_key": "bob"}}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/v1/cli-auth/code" {
			var input api.CliAuthCodeRequest
			if json.NewDecoder(req.Body).Decode(&input) != nil || input.Purpose != api.CliAuthPurposeMCPRead {
				t.Error("unexpected native purpose")
			}
			_ = json.NewEncoder(w).Encode(api.CliAuthCodeResponse{Code: "MCPR-fixture", URL: "https://console.example/cli-auth?code=MCPR-fixture", ExpiresAt: time.Now().Add(time.Minute).Format(time.RFC3339)})
			return
		}
		if req.URL.Path == "/v1/cli-auth/exchange" {
			var input api.CliAuthExchangeRequest
			if json.NewDecoder(req.Body).Decode(&input) != nil || input.Code != "MCPR-fixture" {
				t.Error("unexpected native code")
			}
			_ = json.NewEncoder(w).Encode(api.CliAuthExchangeResponse{Plaintext: "fp_test_alice_remote_key", Account: api.AccountResponse{ID: "alice"}, ExpiresAt: time.Now().Add(time.Hour).Format(time.RFC3339)})
			return
		}
		f.mu.Lock()
		account := f.accounts[strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")]
		hook := f.apiHook
		f.mu.Unlock()
		if account == "" {
			api.WriteProblem(w, api.NewProblem(401, "unauthorized", "Invalid credential", "Credential revoked"))
			return
		}
		if hook != nil && hook(w, req, account) {
			return
		}
		switch req.URL.Path {
		case "/v1/deployments/release-base":
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "release-base", AppID: account + "-app", Kind: "image", Status: "live", Scope: "default", TrafficPercent: 100, RolloutState: "complete"})
		case "/v1/apps/alpha/route-budgets":
			if req.Method != http.MethodGet || req.URL.Query().Get("scope") != "default" {
				w.WriteHeader(400)
				return
			}
			_ = json.NewEncoder(w).Encode(api.EnvironmentRouteBudgets{AppID: account + "-app", Scope: "default", BudgetSource: "app_policy", Config: api.RouteMonitorConfig{AppID: account + "-app", Routes: []api.RouteMonitorRoute{}}})
		case "/v1/apps/alpha/routes":
			if req.URL.Query().Get("scope") != "default" {
				w.WriteHeader(400)
				return
			}
			_ = json.NewEncoder(w).Encode(api.AppRoutesResponse{AppID: account + "-app", Scope: "default", Coverage: "deployment_attributed_history", Source: "unavailable", Routes: []string{}})
		case "/v1/apps/alpha/operational-summary":
			if req.URL.Query().Get("scope") != "default" {
				w.WriteHeader(400)
				return
			}
			_ = json.NewEncoder(w).Encode(api.AppOperationalSummary{Version: 1, AppID: account + "-app", Scope: "default", Recovery: api.AppOperationalRecovery{Coverage: "explicit_scope_only", Rollbacks: []api.AppOperationalRollback{}, Restarts: []api.RuntimeConfigRestartStatusResponse{}}})
		case "/v1/apps/alpha/source-review":
			var input api.SourceReviewRequest
			if json.NewDecoder(req.Body).Decode(&input) != nil {
				w.WriteHeader(400)
				return
			}
			_ = json.NewEncoder(w).Encode(api.SourceReviewResponse{AppID: account + "-app", Repo: input.Repo, CommitSHA: strings.Repeat("a", 40), InstallationID: 55, RepositoryID: 987, ReleaseReview: api.AppReleaseReview{AppID: account + "-app", ConfigurationHash: strings.Repeat("a", 64), LatestDeploymentID: "release-base", ServingDeploymentIDs: []string{"release-base"}}})
		case "/v1/projects":
			_ = json.NewEncoder(w).Encode([]api.ProjectSummaryResponse{{ID: account + "-project", Slug: "shop"}})
		case "/v1/projects/shop":
			_ = json.NewEncoder(w).Encode(api.ProjectResponse{ProjectSummaryResponse: api.ProjectSummaryResponse{ID: account + "-project", Slug: "shop"}, Workloads: []api.ProjectWorkloadResponse{{Slug: "alpha"}}})
		case "/v1/projects/shop/environments":
			_ = json.NewEncoder(w).Encode([]api.ProjectEnvironmentResponse{{ID: account + "-staging", ProjectID: account + "-project", Slug: "staging"}, {ID: account + "-production", ProjectID: account + "-project", Slug: "production", Protected: true}})
		case "/v1/projects/shop/environments/staging", "/v1/projects/shop/environments/production":
			slug := "staging"
			if strings.HasSuffix(req.URL.Path, "production") {
				slug = "production"
			}
			_ = json.NewEncoder(w).Encode(api.ProjectEnvironmentResponse{ID: account + "-" + slug, ProjectID: account + "-project", Slug: slug, Protected: slug == "production"})
		case "/v1/account":
			_ = json.NewEncoder(w).Encode(api.AccountResponse{ID: account, Plan: "free", Status: "active"})
		case "/v1/capabilities":
			_ = json.NewEncoder(w).Encode(api.CapabilitiesResponse{Plan: "free"})
		case "/v1/apps":
			_ = json.NewEncoder(w).Encode([]api.AppResponse{{ID: account + "-app", Slug: "alpha"}})
		case "/v1/apps/alpha":
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: account + "-app", Slug: "alpha", Type: "app"})
		case "/v1/apps/alpha/release-review":
			_ = json.NewEncoder(w).Encode(api.AppReleaseReview{AppID: account + "-app", ConfigurationHash: strings.Repeat("a", 64), LatestDeploymentID: "release-base", ServingDeploymentIDs: []string{"release-base"}})
		case "/v1/apps/alpha/health":
			_ = json.NewEncoder(w).Encode(api.AppHealthResponse{AppID: account + "-app", Scope: "default", ServingDeploymentIDs: []string{"release-base"}, LatestDeploymentID: "release-base"})
		default:
			t.Errorf("unexpected remote API call: %s %s", req.Method, req.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(backend.Close)
	var handler http.Handler
	var handlerMu sync.RWMutex
	f.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		handlerMu.RLock()
		h := handler
		handlerMu.RUnlock()
		h.ServeHTTP(w, req)
	}))
	t.Cleanup(f.server.Close)
	f.resource = f.server.URL + "/mcp"
	options.APIBase, options.ResourceURL, options.AllowWrites = backend.URL, f.resource, writes
	if len(options.RedirectURIs) == 0 {
		options.RedirectURIs = []string{testRedirect}
	}
	f.opts = options
	h, err := NewHTTPHandler(options)
	if err != nil {
		t.Fatal(err)
	}
	f.setHandler = func(h http.Handler) { handlerMu.Lock(); handler = h; handlerMu.Unlock() }
	f.setHandler(h)
	f.client = f.server.Client()
	f.client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response := f.request(http.MethodPost, "/oauth/register", strings.NewReader(`{"client_name":"test-agent","redirect_uris":["`+testRedirect+`"],"grant_types":["authorization_code","refresh_token"],"token_endpoint_auth_method":"none"}`), "application/json", "")
	if response.StatusCode != 201 {
		t.Fatalf("registration=%d", response.StatusCode)
	}
	var registered struct {
		ClientID string `json:"client_id"`
	}
	decodeResponse(t, response, &registered)
	f.clientID = registered.ClientID
	return f
}

func decodeResponse(t *testing.T, response *http.Response, out any) {
	t.Helper()
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(out); err != nil {
		t.Fatal(err)
	}
}

func (f *remoteFixture) request(method, path string, body io.Reader, contentType, token string) *http.Response {
	f.t.Helper()
	req, err := http.NewRequest(method, f.server.URL+path, body)
	if err != nil {
		f.t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := f.client.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	return response
}

type browserFlow struct {
	client    *http.Client
	requestID string
	csrf      string
}

var hiddenInput = regexp.MustCompile(`name="(request_id|csrf)" value="([^"]+)"`)

func (f *remoteFixture) begin(scope string) browserFlow {
	f.t.Helper()
	sum := sha256.Sum256([]byte(testVerifier))
	query := url.Values{"client_id": {f.clientID}, "redirect_uri": {testRedirect}, "response_type": {"code"}, "resource": {f.resource}, "scope": {scope}, "state": {"client-state"}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}}
	return f.beginAt(context.Background(), f.server.URL+"/oauth/authorize?"+query.Encode())
}

func (f *remoteFixture) beginAt(ctx context.Context, authorizationURL string) browserFlow {
	f.t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		f.t.Fatal(err)
	}
	client := *f.client
	client.Jar = jar
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, authorizationURL, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	response, err := client.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != 200 {
		f.t.Fatalf("authorize=%d error=%v", response.StatusCode, err)
	}
	flow := browserFlow{client: &client}
	for _, match := range hiddenInput.FindAllStringSubmatch(string(data), -1) {
		if match[1] == "request_id" {
			flow.requestID = match[2]
		} else {
			flow.csrf = match[2]
		}
	}
	if flow.requestID == "" || flow.csrf == "" {
		f.t.Fatal("authorization form lacks browser binding")
	}
	return flow
}

func (f *remoteFixture) consent(flow browserFlow, decision, key, origin, csrf string, writes bool, selected ...string) *http.Response {
	f.t.Helper()
	form := url.Values{"app_access": {"all"}, "request_id": {flow.requestID}, "csrf": {csrf}, "decision": {decision}}
	if len(selected) > 0 {
		form.Set("app_access", "selected")
		form["apps"] = selected
	}
	if key != "" {
		form.Set("api_key", key)
	}
	if writes {
		form.Set("allow_writes", "yes")
		form.Set("default_write", "yes")
	}
	return f.submitConsent(flow, form, origin)
}

func (f *remoteFixture) submitConsent(flow browserFlow, form url.Values, origin string) *http.Response {
	f.t.Helper()
	req, err := http.NewRequest(http.MethodPost, f.server.URL+"/oauth/consent", strings.NewReader(form.Encode()))
	if err != nil {
		f.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	response, err := flow.client.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	return response
}

func (f *remoteFixture) authorizeAccount(key string, writes bool) string {
	f.t.Helper()
	scope := scopeRead
	if writes {
		scope += " " + scopeWrite
	}
	flow := f.begin(scope)
	response := f.consent(flow, "verify", key, f.server.URL, flow.csrf, false)
	data, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || response.StatusCode != 200 || bytes.Contains(data, []byte(key)) || !bytes.Contains(data, []byte("Connect this account")) {
		f.t.Fatalf("account review=%d error=%v", response.StatusCode, err)
	}
	response = f.consent(flow, "approve", "", f.server.URL, flow.csrf, writes)
	defer response.Body.Close()
	if response.StatusCode != 303 {
		f.t.Fatalf("consent=%d", response.StatusCode)
	}
	redirect, err := url.Parse(response.Header.Get("Location"))
	if err != nil || redirect.Query().Get("state") != "client-state" {
		f.t.Fatalf("redirect=%s error=%v", response.Header.Get("Location"), err)
	}
	return redirect.Query().Get("code")
}

func (f *remoteFixture) exchange(code, verifier, resource, clientID string) *http.Response {
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {verifier}, "resource": {resource}, "client_id": {clientID}, "redirect_uri": {testRedirect}}
	return f.request(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()), "application/x-www-form-urlencoded", "")
}

func (f *remoteFixture) connect(tokens tokenResponse, version string) (*mcp.ClientSession, context.Context) {
	f.t.Helper()
	client := *f.client
	client.Transport = bearerTransport{base: f.client.Transport, token: tokens.AccessToken}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	f.t.Cleanup(cancel)
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "remote-acceptance", Version: "1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: f.resource, HTTPClient: &client, DisableStandaloneSSE: true}, &mcp.ClientSessionOptions{ProtocolVersion: version})
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { _ = cs.Close() })
	return cs, ctx
}

type bearerTransport struct {
	base  http.RoundTripper
	token string
}

func (b bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	copyReq := req.Clone(req.Context())
	copyReq.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(copyReq)
}

func TestRemoteManagementOAuthAccountIsolation(t *testing.T) {
	for _, version := range []string{"2026-07-28", "2025-11-25"} {
		t.Run(version, func(t *testing.T) {
			f := newRemoteFixture(t, true)
			var alice, bob tokenResponse
			response := f.exchange(f.authorizeAccount("fp_test_alice_remote_key", true), testVerifier, f.resource, f.clientID)
			decodeResponse(t, response, &alice)
			_ = response.Body.Close()
			response = f.exchange(f.authorizeAccount("fp_test_bob_remote_key", false), testVerifier, f.resource, f.clientID)
			decodeResponse(t, response, &bob)
			_ = response.Body.Close()
			aliceSession, ctx := f.connect(alice, version)
			bobSession, _ := f.connect(bob, version)
			aliceCatalog, err := aliceSession.ListTools(ctx, nil)
			if err != nil || len(aliceCatalog.Tools) != 66 {
				t.Fatalf("alice tools=%+v error=%v", aliceCatalog, err)
			}
			bobCatalog, err := bobSession.ListTools(ctx, nil)
			if err != nil || len(bobCatalog.Tools) != 57 {
				t.Fatalf("bob tools=%+v error=%v", bobCatalog, err)
			}
			aliceContext := callTool(t, ctx, aliceSession, "gregale_context", map[string]any{}, false)
			bobContext := callTool(t, ctx, bobSession, "gregale_context", map[string]any{}, false)
			if !strings.Contains(string(aliceContext["account"]), "alice") || !strings.Contains(string(bobContext["account"]), "bob") || string(bobContext["read_only"]) != "true" {
				t.Fatal("account credential or write scope leaked between grants")
			}
			plan := callTool(t, ctx, aliceSession, "gregale_plan_deploy", map[string]any{"app": "alpha", "image": testImage, "reason": "Release"}, false)
			// A second write grant for the other account cannot use Alice's plan.
			var bobWrite tokenResponse
			response = f.exchange(f.authorizeAccount("fp_test_bob_remote_key", true), testVerifier, f.resource, f.clientID)
			decodeResponse(t, response, &bobWrite)
			_ = response.Body.Close()
			bobWriteSession, _ := f.connect(bobWrite, version)
			out := callTool(t, ctx, bobWriteSession, "gregale_apply_deploy", map[string]any{"plan_id": planID(t, plan)}, true)
			if string(out["code"]) != `"invalid_plan"` {
				t.Fatalf("cross-account plan=%s", out)
			}
			f.mu.Lock()
			delete(f.accounts, "fp_test_alice_remote_key")
			f.mu.Unlock()
			if _, err := aliceSession.CallTool(ctx, &mcp.CallToolParams{Name: "gregale_context", Arguments: map[string]any{}}); err == nil {
				t.Fatal("revoked API credential still authorized remote requests")
			}
		})
	}
}

func TestRemoteOAuthPKCEResourceAndRefreshBinding(t *testing.T) {
	f := newRemoteFixture(t, false)
	code := f.authorizeAccount("fp_test_alice_remote_key", false)
	for _, tc := range []struct{ verifier, resource, client string }{
		{"wrong", f.resource, f.clientID}, {strings.Repeat("z", 64), f.resource, f.clientID}, {testVerifier, "https://different.example/mcp", f.clientID}, {testVerifier, f.resource, "other-client"},
	} {
		response := f.exchange(code, tc.verifier, tc.resource, tc.client)
		_ = response.Body.Close()
		if response.StatusCode != 400 {
			t.Fatalf("invalid exchange=%d", response.StatusCode)
		}
	}
	wrongRedirect := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {testVerifier}, "resource": {f.resource}, "client_id": {f.clientID}, "redirect_uri": {"https://different.example/callback"}}
	wrongResponse := f.request(http.MethodPost, "/oauth/token", strings.NewReader(wrongRedirect.Encode()), "application/x-www-form-urlencoded", "")
	_ = wrongResponse.Body.Close()
	if wrongResponse.StatusCode != 400 {
		t.Fatal("code exchange accepted a different redirect")
	}
	var initial tokenResponse
	response := f.exchange(code, testVerifier, f.resource, f.clientID)
	decodeResponse(t, response, &initial)
	_ = response.Body.Close()
	if initial.AccessToken == "" || initial.RefreshToken == "" || initial.Scope != scopeRead || initial.ExpiresIn > int(oauthAccessTTL.Seconds()) {
		t.Fatalf("tokens=%+v", initial)
	}
	response = f.exchange(code, testVerifier, f.resource, f.clientID)
	_ = response.Body.Close()
	if response.StatusCode != 400 {
		t.Fatal("authorization code was reusable")
	}
	form := url.Values{"grant_type": {"refresh_token"}, "resource": {f.resource}, "client_id": {f.clientID}, "refresh_token": {initial.RefreshToken}}
	var rotated tokenResponse
	response = f.request(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()), "application/x-www-form-urlencoded", "")
	if response.StatusCode != 200 {
		t.Fatalf("refresh=%d", response.StatusCode)
	}
	decodeResponse(t, response, &rotated)
	if rotated.AccessToken == initial.AccessToken || rotated.RefreshToken == initial.RefreshToken {
		t.Fatal("refresh failed to rotate tokens")
	}
	response = f.request(http.MethodPost, "/mcp", strings.NewReader(`{}`), "application/json", initial.AccessToken)
	_ = response.Body.Close()
	if response.StatusCode != 401 {
		t.Fatal("old access token survived rotation")
	}
	response = f.request(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()), "application/x-www-form-urlencoded", "")
	_ = response.Body.Close()
	if response.StatusCode != 400 {
		t.Fatal("refresh replay was accepted")
	}
	response = f.request(http.MethodPost, "/mcp", strings.NewReader(`{}`), "application/json", rotated.AccessToken)
	_ = response.Body.Close()
	if response.StatusCode != 401 {
		t.Fatal("refresh replay did not revoke the grant")
	}
}

func TestRemoteOAuthConsentAndHTTPGuards(t *testing.T) {
	f := newRemoteFixture(t, true)
	flow := f.begin(scopeRead + " " + scopeWrite)
	for _, tc := range []struct{ origin, csrf string }{{"", flow.csrf}, {"https://attacker.example", flow.csrf}, {f.server.URL, "forged"}} {
		response := f.consent(flow, "verify", "fp_test_alice_remote_key", tc.origin, tc.csrf, false)
		_ = response.Body.Close()
		if response.StatusCode != 403 {
			t.Fatalf("forged consent=%d", response.StatusCode)
		}
	}
	response := f.consent(flow, "approve", "", f.server.URL, flow.csrf, true)
	_ = response.Body.Close()
	if response.StatusCode != 400 {
		t.Fatal("approved account without account review")
	}
	response = f.consent(flow, "verify", "fp_test_alice_remote_key", f.server.URL, flow.csrf, false)
	_ = response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("could not review account")
	}
	response = f.consent(flow, "approve", "", f.server.URL, flow.csrf, false)
	_ = response.Body.Close()
	if response.StatusCode != 400 {
		t.Fatal("writes granted without explicit checkbox")
	}
	response = f.consent(flow, "deny", "", f.server.URL, flow.csrf, false)
	_ = response.Body.Close()
	if response.StatusCode != 303 || !strings.Contains(response.Header.Get("Location"), "access_denied") {
		t.Fatal("could not decline grant")
	}
	for _, path := range []string{"/.well-known/oauth-protected-resource/mcp", "/.well-known/oauth-authorization-server"} {
		response = f.request(http.MethodGet, path, nil, "", "")
		_ = response.Body.Close()
		if response.StatusCode != 200 || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("metadata=%d", response.StatusCode)
		}
	}
	response = f.request(http.MethodPost, "/mcp", strings.NewReader(`{}`), "application/json", "fp_test_alice_remote_key")
	_ = response.Body.Close()
	if response.StatusCode != 401 || !strings.Contains(response.Header.Get("WWW-Authenticate"), "resource_metadata=") {
		t.Fatal("API keys were accepted as MCP tokens or OAuth discovery was missing")
	}
	req, err := http.NewRequest(http.MethodGet, f.server.URL+"/.well-known/oauth-authorization-server", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "attacker.example"
	response, err = f.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal("unrecognized Host accepted")
	}
	response = f.request(http.MethodPost, "/oauth/register", strings.NewReader(`{"redirect_uris":["https://attacker.example/callback"]}`), "application/json", "")
	_ = response.Body.Close()
	if response.StatusCode != 400 {
		t.Fatal("unapproved redirect registered")
	}
}

func TestRemoteOAuthRevocation(t *testing.T) {
	f := newRemoteFixture(t, false)
	var tokens tokenResponse
	response := f.exchange(f.authorizeAccount("fp_test_alice_remote_key", false), testVerifier, f.resource, f.clientID)
	decodeResponse(t, response, &tokens)
	_ = response.Body.Close()
	form := url.Values{"client_id": {f.clientID}, "token": {tokens.RefreshToken}}
	response = f.request(http.MethodPost, "/oauth/revoke", strings.NewReader(form.Encode()), "application/x-www-form-urlencoded", "")
	_ = response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("could not revoke grant")
	}
	response = f.request(http.MethodPost, "/mcp", strings.NewReader(`{}`), "application/json", tokens.AccessToken)
	_ = response.Body.Close()
	if response.StatusCode != 401 {
		t.Fatal("revoked grant still accepted")
	}
}

func TestRemoteManagementRejectsUnsafeConfiguration(t *testing.T) {
	for _, opts := range []HTTPOptions{
		{ResourceURL: "http://example.test/mcp", APIBase: "https://api.example", RedirectURIs: []string{testRedirect}},
		{ResourceURL: "https://example.test/mcp", APIBase: "https://api.example"},
		{ResourceURL: "https://example.test/mcp", APIBase: "http://api.example", RedirectURIs: []string{testRedirect}},
		{ResourceURL: "https://example.test/mcp", APIBase: "https://api.example", RedirectURIs: []string{"http://client.example/callback"}},
		{ResourceURL: "https://example.test/mcp?secret=x", APIBase: "https://api.example", RedirectURIs: []string{testRedirect}},
	} {
		if _, err := NewHTTPHandler(opts); err == nil {
			t.Fatalf("unsafe configuration accepted: %+v", opts)
		}
	}
}

func TestRemoteOAuthLoopbackPortAllowlist(t *testing.T) {
	r := &remoteServer{opts: HTTPOptions{RedirectURIs: []string{"http://127.0.0.1/callback", testRedirect}}}
	for _, tc := range []struct {
		value   string
		allowed bool
	}{
		{"http://127.0.0.1:43123/callback", true}, {testRedirect, true},
		{"http://127.0.0.1:43123/other", false}, {"http://127.0.0.1:43123/callback?next=evil", false},
		{"http://localhost:43123/callback", false}, {"http://127.0.0.1.attacker.example/callback", false},
		{"https://client.example/oauth/callback?next=evil", false}, {"https://client.example/oauth/callback#evil", false},
	} {
		if got := r.redirectAllowed(tc.value); got != tc.allowed {
			t.Errorf("redirect=%s allowed=%v want=%v", tc.value, got, tc.allowed)
		}
	}
}

func TestRemoteManagementOfficialSDKOAuthDiscoveryAndRefresh(t *testing.T) {
	f := newRemoteFixture(t, false)
	handler, err := auth.NewAuthorizationCodeHandler(&auth.AuthorizationCodeHandlerConfig{
		Client:                          f.client,
		RedirectURL:                     testRedirect,
		RequestRefreshToken:             true,
		DynamicClientRegistrationConfig: &auth.DynamicClientRegistrationConfig{Metadata: &oauthex.ClientRegistrationMetadata{ClientName: "official-sdk", RedirectURIs: []string{testRedirect}, GrantTypes: []string{"authorization_code", "refresh_token"}, TokenEndpointAuthMethod: "none"}},
		AuthorizationCodeFetcher: func(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
			flow := f.beginAt(ctx, args.URL)
			response := f.consent(flow, "verify", "fp_test_alice_remote_key", f.server.URL, flow.csrf, false)
			_ = response.Body.Close()
			if response.StatusCode != 200 {
				t.Fatalf("SDK account review=%d", response.StatusCode)
			}
			response = f.consent(flow, "approve", "", f.server.URL, flow.csrf, false)
			defer response.Body.Close()
			location, err := response.Location()
			if err != nil {
				return nil, err
			}
			return &auth.AuthorizationResult{Code: location.Query().Get("code"), State: location.Query().Get("state")}, nil
		},
		NewTokenSource: func(ctx context.Context, config *oauth2.Config, token *oauth2.Token) (oauth2.TokenSource, error) {
			// Force a standard-library refresh before the retried MCP request.
			// That client omits resource on refresh, inheriting its bound grant.
			token.Expiry = time.Now().Add(-time.Second)
			return config.TokenSource(ctx, token), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "oauth-discovery", Version: "1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: f.resource, HTTPClient: f.client, OAuthHandler: handler, DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cs.Close() }()
	out := callTool(t, ctx, cs, "gregale_context", map[string]any{}, false)
	if !strings.Contains(string(out["account"]), "alice") || string(out["read_only"]) != "true" {
		t.Fatalf("SDK OAuth connection=%s", out)
	}
}
