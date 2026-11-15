// adr: 20261030065746
package mcpmanagement

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const antigravityClientID = "https://antigravity.google/oauth/client-metadata.json"
const antigravityCallback = "https://antigravity.google/oauth-callback"

// Match the production unit, rather than a test-only redirect list. This pins
// the configuration that rejected Antigravity before account sign-in.
func packagedBridgeRedirects(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "deploy", "ansible", "roles", "control_plane_service", "files", "faas-mcp-bridge.service"))
	if err != nil {
		t.Fatal(err)
	}
	var redirects []string
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "ExecStart=") {
			continue
		}
		fields := strings.Fields(line)
		for i := 0; i+1 < len(fields); i++ {
			if fields[i] == "--oauth-redirect-uri" {
				redirects = append(redirects, fields[i+1])
			}
		}
	}
	if len(redirects) == 0 {
		t.Fatal("packaged bridge has no redirect policy")
	}
	return redirects
}

type antigravityMetadataTransport struct{ t *testing.T }

func (tr antigravityMetadataTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	tr.t.Helper()
	if r.Method != http.MethodGet || r.URL.String() != antigravityClientID {
		tr.t.Fatalf("unexpected metadata request %s %s", r.Method, r.URL)
	}
	// Public client document verified at the URL above on 2026-11-15.
	doc := `{"client_id":"https://antigravity.google/oauth/client-metadata.json","client_name":"Google Antigravity","redirect_uris":["https://antigravity.google/oauth-callback"],"grant_types":["authorization_code","refresh_token"],"response_types":["code"],"token_endpoint_auth_method":"none"}`
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(doc)), Request: r}, nil
}

func TestAntigravityOAuthWithPackagedRedirectPolicy(t *testing.T) {
	for _, version := range []string{"2026-07-28", "2025-11-25"} {
		t.Run(version, func(t *testing.T) {
			redirects := packagedBridgeRedirects(t)
			policy := &remoteServer{opts: HTTPOptions{RedirectURIs: redirects}}
			if !policy.redirectAllowed(antigravityCallback) {
				t.Fatal("packaged bridge blocks Antigravity's published callback")
			}
			for _, uri := range []string{"https://antigravity.google/oauth-callback/extra", "https://antigravity.google/oauth-callback?next=evil", "https://antigravity.google.evil.example/oauth-callback", "http://antigravity.google/oauth-callback"} {
				if policy.redirectAllowed(uri) {
					t.Fatalf("redirect policy widened to %s", uri)
				}
			}
			// The shared fixture first registers its own client; it remains
			// unrelated to the metadata-document client exercised below.
			f := newRemoteFixtureWithOptions(t, false, HTTPOptions{Onboarding: "api-key", ClientMetadataDocuments: true, RedirectURIs: append(redirects, testRedirect)})
			service, err := NewHTTPService(f.opts)
			if err != nil {
				t.Fatal(err)
			}
			service.remote.metadataClient = &http.Client{Transport: antigravityMetadataTransport{t}}
			f.setHandler(service.Handler())
			authorize, err := url.Parse(authorizeURL(f, antigravityClientID))
			if err != nil {
				t.Fatal(err)
			}
			q := authorize.Query()
			q.Set("redirect_uri", antigravityCallback)
			authorize.RawQuery = q.Encode()
			flow := f.beginAt(context.Background(), authorize.String())
			response := f.consent(flow, "verify", "fp_test_alice_remote_key", f.server.URL, flow.csrf, false)
			_ = response.Body.Close()
			if response.StatusCode != http.StatusOK {
				t.Fatalf("account review=%d", response.StatusCode)
			}
			response = f.consent(flow, "approve", "", f.server.URL, flow.csrf, false)
			_ = response.Body.Close()
			callback, err := url.Parse(response.Header.Get("Location"))
			if err != nil || response.StatusCode != http.StatusSeeOther || callback.Scheme+"://"+callback.Host+callback.Path != antigravityCallback || callback.Query().Get("state") != "client-state" || callback.Query().Get("code") == "" {
				t.Fatal("authorization did not return to Antigravity")
			}
			form := url.Values{"grant_type": {"authorization_code"}, "code": {callback.Query().Get("code")}, "code_verifier": {testVerifier}, "resource": {f.resource}, "client_id": {antigravityClientID}, "redirect_uri": {antigravityCallback}}
			response = f.request(http.MethodPost, f.prefix+"/oauth/token", strings.NewReader(form.Encode()), "application/x-www-form-urlencoded", "")
			var tokens tokenResponse
			decodeResponse(t, response, &tokens)
			_ = response.Body.Close()
			if response.StatusCode != http.StatusOK || tokens.AccessToken == "" {
				t.Fatalf("token exchange=%d", response.StatusCode)
			}
			client, ctx := f.connect(tokens, version)
			defer client.Close()
			catalog, err := client.ListTools(ctx, nil)
			if err != nil || len(catalog.Tools) == 0 {
				t.Fatalf("authenticated MCP initialization/discovery: %v", err)
			}
		})
	}
}
