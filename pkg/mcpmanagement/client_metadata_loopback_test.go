// adr: 20261030065746
package mcpmanagement

import (
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

const loopbackCallback = "http://localhost/callback"

func authorizeURLWithRedirect(f *remoteFixture, clientID, redirect string) string {
	sum := sha256.Sum256([]byte(testVerifier))
	query := url.Values{"client_id": {clientID}, "redirect_uri": {redirect}, "response_type": {"code"}, "resource": {f.resource}, "scope": {scopeRead}, "state": {"client-state"}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}}
	return f.server.URL + f.prefix + "/oauth/authorize?" + query.Encode()
}

// metadataAuthorize serves doc as a client metadata document and returns the
// authorize response for redirect.
func metadataAuthorize(t *testing.T, doc func(id string) map[string]any, redirect string) (int, string) {
	t.Helper()
	f := newRemoteFixtureWithOptions(t, false, HTTPOptions{Onboarding: "api-key", ClientMetadataDocuments: true, RedirectURIs: []string{testRedirect, loopbackCallback}})
	service, err := NewHTTPService(f.opts)
	if err != nil {
		t.Fatal(err)
	}
	f.setHandler(service.Handler())
	docs, _ := metadataDocumentServer(t, doc)
	service.remote.metadataClient = docs.Client()
	response, err := f.client.Get(authorizeURLWithRedirect(f, docs.URL+"/oauth/client.json", redirect))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	return response.StatusCode, string(body)
}

// Claude Code publishes http://localhost/callback and listens on an ephemeral
// port; the server must accept any port on that loopback host and path
// (RFC 8252 §7.3), and nothing else.
func TestMetadataDocumentLoopbackRedirectsMatchAnyPort(t *testing.T) {
	claudeCode := func(id string) map[string]any {
		return map[string]any{"client_id": id, "client_name": "Claude Code", "redirect_uris": []string{loopbackCallback, "http://127.0.0.1/callback"}, "grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"}, "token_endpoint_auth_method": "none"}
	}
	for redirect, want := range map[string]int{
		"http://localhost:53211/callback":  http.StatusOK,
		"http://localhost/callback":        http.StatusOK,
		"http://localhost:53211/other":     http.StatusBadRequest,
		"http://localhost:53211/callback/": http.StatusBadRequest,
		"https://localhost:53211/callback": http.StatusBadRequest,
		// The operator allows only localhost; the document's 127.0.0.1 entry
		// was dropped, so its ported form is refused too.
		"http://127.0.0.1:53211/callback": http.StatusBadRequest,
	} {
		t.Run(redirect, func(t *testing.T) {
			status, body := metadataAuthorize(t, claudeCode, redirect)
			if status != want {
				t.Fatalf("authorize(%s) = %d %s, want %d", redirect, status, body, want)
			}
		})
	}
}

// A document may list grants this server does not issue; only one without
// authorization_code cannot sign in here.
func TestMetadataDocumentGrantTypes(t *testing.T) {
	for name, tc := range map[string]struct {
		grants []string
		want   int
	}{
		"code and refresh":  {[]string{"authorization_code", "refresh_token"}, http.StatusOK},
		"extra device code": {[]string{"authorization_code", "refresh_token", "urn:ietf:params:oauth:grant-type:device_code"}, http.StatusOK},
		"none listed":       {nil, http.StatusOK},
		"no code grant":     {[]string{"client_credentials"}, http.StatusBadRequest},
	} {
		t.Run(name, func(t *testing.T) {
			doc := func(id string) map[string]any {
				d := map[string]any{"client_id": id, "redirect_uris": []string{testRedirect}, "token_endpoint_auth_method": "none"}
				if tc.grants != nil {
					d["grant_types"] = tc.grants
				}
				return d
			}
			status, body := metadataAuthorize(t, doc, testRedirect)
			if status != tc.want {
				t.Fatalf("authorize = %d %s, want %d", status, body, tc.want)
			}
			if tc.want == http.StatusBadRequest && !strings.Contains(body, "authorization_code") {
				t.Fatalf("refusal does not say what is missing: %s", body)
			}
		})
	}
}

// Registration and metadata documents accept the same grant lists.
func TestGrantTypesAllowed(t *testing.T) {
	r := &remoteServer{opts: HTTPOptions{RedirectURIs: []string{testRedirect}}}
	for name, tc := range map[string]struct {
		grants []string
		want   bool
	}{
		"code and refresh":  {[]string{"authorization_code", "refresh_token"}, true},
		"extra device code": {[]string{"authorization_code", "urn:ietf:params:oauth:grant-type:device_code"}, true},
		"none listed":       {nil, true},
		"no code grant":     {[]string{"client_credentials"}, false},
	} {
		if got := grantTypesAllowed(tc.grants); got != tc.want {
			t.Errorf("%s: grantTypesAllowed = %v, want %v", name, got, tc.want)
		}
		registration := registrationRequest{RedirectURIs: []string{testRedirect}, GrantTypes: tc.grants}
		if got := r.validRegistration(registration); got != tc.want {
			t.Errorf("%s: validRegistration = %v, want %v", name, got, tc.want)
		}
	}
}

// Clients that registered name their exact redirect URI; only metadata
// document clients get the loopback port allowance.
func TestRegisteredClientsKeepExactRedirects(t *testing.T) {
	registered := &oauthClient{ID: "gmcp_client_x", Redirects: []string{loopbackCallback}}
	if clientRedirectMatches(registered, "http://localhost:53211/callback") {
		t.Fatal("registered client matched a port it did not register")
	}
	if !clientRedirectMatches(registered, loopbackCallback) {
		t.Fatal("registered client did not match its exact redirect")
	}
}
