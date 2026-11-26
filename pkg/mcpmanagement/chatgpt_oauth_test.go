// adr: 20261030065746
package mcpmanagement

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// ChatGPT's client metadata document as published at its client_id URL on
// 2026-11-26. It lists both methods it can use (MCP SEP-3149) and keeps the
// legacy singular field as a preference for private_key_jwt.
const (
	chatGPTClientID = "https://chatgpt.com/oauth/client.json"
	chatGPTCallback = "https://chatgpt.com/connector_platform_oauth_redirect"
	chatGPTDocument = `{"client_id":"https://chatgpt.com/oauth/client.json","client_uri":"https://chatgpt.com/","redirect_uris":["https://chatgpt.com/connector_platform_oauth_redirect"],"token_endpoint_auth_method":"private_key_jwt","token_endpoint_auth_methods_supported":["none","private_key_jwt"],"grant_types":["authorization_code","refresh_token","urn:ietf:params:oauth:grant-type:jwt-bearer"],"response_types":["code"],"client_name":"ChatGPT","logo_uri":"https://persistent.oaistatic.com/sonic/misc/openai-logo.png","token_endpoint_auth_signing_alg":"RS256","jwks_uri":"https://chatgpt.com/oauth/jwks.json"}`
)

// The authorize endpoint used to refuse this document with "only public
// clients (token_endpoint_auth_method none) are supported", so the OpenAI
// plugin portal could not connect the hosted server or discover its tools.
func TestChatGPTOAuthWithPackagedRedirectPolicy(t *testing.T) {
	redirects := packagedBridgeRedirects(t)
	policy := &remoteServer{opts: HTTPOptions{RedirectURIs: redirects}}
	if !policy.redirectAllowed(chatGPTCallback) {
		t.Fatalf("packaged bridge blocks ChatGPT's published callback %s", chatGPTCallback)
	}

	f := newRemoteFixtureWithOptions(t, false, HTTPOptions{Onboarding: "api-key", ClientMetadataDocuments: true, RedirectURIs: append(redirects, testRedirect)})
	service, err := NewHTTPService(f.opts)
	if err != nil {
		t.Fatal(err)
	}
	service.remote.metadataClient = &http.Client{Transport: publishedMetadataTransport{t: t, clientID: chatGPTClientID, document: chatGPTDocument}}
	f.setHandler(service.Handler())
	authorize, err := url.Parse(authorizeURL(f, chatGPTClientID))
	if err != nil {
		t.Fatal(err)
	}
	q := authorize.Query()
	q.Set("redirect_uri", chatGPTCallback)
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
	if err != nil || response.StatusCode != http.StatusSeeOther || callback.Scheme+"://"+callback.Host+callback.Path != chatGPTCallback || callback.Query().Get("code") == "" {
		t.Fatal("authorization did not return to ChatGPT")
	}
	form := url.Values{"grant_type": {"authorization_code"}, "code": {callback.Query().Get("code")}, "code_verifier": {testVerifier}, "resource": {f.resource}, "client_id": {chatGPTClientID}, "redirect_uri": {chatGPTCallback}}
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
}
