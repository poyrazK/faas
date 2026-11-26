// adr: 20261030065746
package mcpmanagement

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// Glama's client metadata documents, as published at their client_id URLs on
// 2026-11-25. The connector document serves Glama's health checks, tool index
// and users' connections; the inspector document serves its MCP Inspector.
var glamaClients = []struct {
	name, clientID, callback, document string
}{
	{
		name:     "connector",
		clientID: "https://glama.ai/api/app/mcp/oauth/client-metadata.json",
		callback: "https://glama.ai/api/app/mcp/oauth/callback",
		document: `{"client_id":"https://glama.ai/api/app/mcp/oauth/client-metadata.json","client_name":"Glama","client_uri":"https://glama.ai/","grant_types":["authorization_code","refresh_token"],"redirect_uris":["https://glama.ai/api/app/mcp/oauth/callback"],"response_types":["code"],"token_endpoint_auth_method":"none"}`,
	},
	{
		name:     "inspector",
		clientID: "https://glama.ai/mcp/inspector/oauth/client-metadata.json",
		callback: "https://glama.ai/mcp/inspector/oauth/callback",
		document: `{"client_id":"https://glama.ai/mcp/inspector/oauth/client-metadata.json","client_name":"Glama MCP Inspector","client_uri":"https://glama.ai/mcp/inspector","grant_types":["authorization_code","refresh_token"],"redirect_uris":["https://glama.ai/mcp/inspector/oauth/callback"],"response_types":["code"],"token_endpoint_auth_method":"none"}`,
	},
}

type publishedMetadataTransport struct {
	t        *testing.T
	clientID string
	document string
}

func (tr publishedMetadataTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	tr.t.Helper()
	if r.Method != http.MethodGet || r.URL.String() != tr.clientID {
		tr.t.Fatalf("unexpected metadata request %s %s", r.Method, r.URL)
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(tr.document)), Request: r}, nil
}

// The shipped unit used to refuse Glama's documents with "none of the
// client's redirect URIs is allowed on this server", so Glama could neither
// health-check the connector nor index its tools.
func TestGlamaOAuthWithPackagedRedirectPolicy(t *testing.T) {
	redirects := packagedBridgeRedirects(t)
	policy := &remoteServer{opts: HTTPOptions{RedirectURIs: redirects}}
	for _, client := range glamaClients {
		t.Run(client.name, func(t *testing.T) {
			if !policy.redirectAllowed(client.callback) {
				t.Fatalf("packaged bridge blocks Glama's published callback %s", client.callback)
			}
			for _, uri := range []string{client.callback + "/extra", client.callback + "?next=evil", strings.Replace(client.callback, "glama.ai", "glama.ai.evil.example", 1), strings.Replace(client.callback, "https://", "http://", 1)} {
				if policy.redirectAllowed(uri) {
					t.Fatalf("redirect policy widened to %s", uri)
				}
			}

			f := newRemoteFixtureWithOptions(t, false, HTTPOptions{Onboarding: "api-key", ClientMetadataDocuments: true, RedirectURIs: append(redirects, testRedirect)})
			service, err := NewHTTPService(f.opts)
			if err != nil {
				t.Fatal(err)
			}
			service.remote.metadataClient = &http.Client{Transport: publishedMetadataTransport{t: t, clientID: client.clientID, document: client.document}}
			f.setHandler(service.Handler())
			authorize, err := url.Parse(authorizeURL(f, client.clientID))
			if err != nil {
				t.Fatal(err)
			}
			q := authorize.Query()
			q.Set("redirect_uri", client.callback)
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
			if err != nil || response.StatusCode != http.StatusSeeOther || callback.Scheme+"://"+callback.Host+callback.Path != client.callback || callback.Query().Get("state") != "client-state" || callback.Query().Get("code") == "" {
				t.Fatal("authorization did not return to Glama")
			}
			form := url.Values{"grant_type": {"authorization_code"}, "code": {callback.Query().Get("code")}, "code_verifier": {testVerifier}, "resource": {f.resource}, "client_id": {client.clientID}, "redirect_uri": {client.callback}}
			response = f.request(http.MethodPost, f.prefix+"/oauth/token", strings.NewReader(form.Encode()), "application/x-www-form-urlencoded", "")
			var tokens tokenResponse
			decodeResponse(t, response, &tokens)
			_ = response.Body.Close()
			if response.StatusCode != http.StatusOK || tokens.AccessToken == "" {
				t.Fatalf("token exchange=%d", response.StatusCode)
			}
			mcpClient, ctx := f.connect(tokens, "2025-11-25")
			defer mcpClient.Close()
			catalog, err := mcpClient.ListTools(ctx, nil)
			if err != nil || len(catalog.Tools) == 0 {
				t.Fatalf("authenticated MCP initialization/discovery: %v", err)
			}
		})
	}
}
