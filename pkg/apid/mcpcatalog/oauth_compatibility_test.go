package mcpcatalog

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestOAuthTokenProfilesAndResources(t *testing.T) {
	for _, method := range []string{"none", "client_secret_basic", "client_secret_post"} {
		t.Run(method, func(t *testing.T) {
			cfg := &api.MCPCatalogObserverOAuthConfig{TokenEndpoint: "https://provider.example/token", ClientID: "reader", TokenAuthMethod: method, Resource: "https://mcp.example/mcp"}
			secret := ""
			if method != "none" {
				secret = "PRIVATE_SECRET"
				cfg.ClientSecretRef = "secret:CLIENT"
			}
			runner := ObserverRunner{Client: &http.Client{Transport: oauthRoundTripper(func(req *http.Request) (*http.Response, error) {
				if err := req.ParseForm(); err != nil {
					t.Fatal(err)
				}
				if req.Form.Get("resource") != cfg.Resource {
					t.Fatal("resource lost")
				}
				_, password, basic := req.BasicAuth()
				if method == "client_secret_basic" && (!basic || password != secret || req.Form.Get("client_secret") != "") {
					t.Fatal("basic credential transport wrong")
				}
				if method == "client_secret_post" && (basic || req.Form.Get("client_secret") != secret) {
					t.Fatal("post credential transport wrong")
				}
				if method == "none" && (basic || req.Form.Get("client_secret") != "") {
					t.Fatal("public client sent credentials")
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"access_token":"ACCESS","refresh_token":"REFRESH","token_type":"Bearer","expires_in":3600}`))}, nil
			})}}
			if _, e := runner.exchangeOAuthCode(t.Context(), cfg, "CODE", "VERIFIER", "http://127.0.0.1:123/callback", secret); e != nil {
				t.Fatal(e)
			}
			if _, _, e := runner.refreshOAuth(t.Context(), cfg, "REFRESH", secret); e != nil {
				t.Fatal(e)
			}
		})
	}
	cfg := &api.MCPCatalogObserverOAuthConfig{Profile: "google", ClientSecretRef: "secret:CLIENT"}
	if cfg.ResolvedTokenAuthMethod() != "client_secret_post" {
		t.Fatal("Google confidential default incorrect")
	}
	scopes, e := compatibleOAuthScopes(cfg, []string{"openid", "email"}, "openid https://www.googleapis.com/auth/userinfo.email")
	if e != nil || len(scopes) != 2 {
		t.Fatal("Google canonical scope aliases rejected", e)
	}
	if _, e = compatibleOAuthScopes(cfg, []string{"email"}, "https://www.googleapis.com/auth/calendar"); e == nil {
		t.Fatal("Google grants broadened")
	}
}
func TestOAuthProviderMetadataCompatibility(t *testing.T) {
	cfg := &api.MCPCatalogObserverOAuthConfig{Guided: true, Issuer: "https://provider.example", DiscoveryURL: "https://provider.example/.well-known/oauth-authorization-server", AuthorizationEndpoint: "https://provider.example/authorize", TokenEndpoint: "https://provider.example/token", ClientID: "reader", Scopes: []string{"read"}}
	baseline := oauthProviderMetadata{Issuer: cfg.Issuer, AuthorizationEndpoint: cfg.AuthorizationEndpoint, TokenEndpoint: cfg.TokenEndpoint, PKCEMethods: []string{"S256"}, AuthMethods: []string{"none"}, Grants: []string{"authorization_code", "refresh_token"}, ResponseTypes: []string{"code"}, Scopes: []string{"read"}}
	for _, tc := range []struct {
		name, state string
		edit        func(*oauthProviderMetadata)
	}{
		{"compatible", "compatible", func(*oauthProviderMetadata) {}},
		{"wrong_issuer", "unsupported", func(m *oauthProviderMetadata) { m.Issuer = "https://other.example" }},
		{"wrong_token_endpoint", "unsupported", func(m *oauthProviderMetadata) { m.TokenEndpoint = "https://other.example/token" }},
		{"plain_pkce", "unsupported", func(m *oauthProviderMetadata) { m.PKCEMethods = []string{"plain"} }},
		{"missing_pkce", "unverified", func(m *oauthProviderMetadata) { m.PKCEMethods = nil }},
		{"wrong_auth_method", "unsupported", func(m *oauthProviderMetadata) { m.AuthMethods = []string{"client_secret_basic"} }},
		{"no_refresh", "unsupported", func(m *oauthProviderMetadata) { m.Grants = []string{"authorization_code"} }},
		{"unadvertised_scope", "unverified", func(m *oauthProviderMetadata) { m.Scopes = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			metadata := baseline
			tc.edit(&metadata)
			raw, _ := json.Marshal(metadata)
			runner := ObserverRunner{Client: &http.Client{Transport: oauthRoundTripper(func(req *http.Request) (*http.Response, error) {
				if req.URL.String() != cfg.DiscoveryURL || req.Header.Get("Authorization") != "" {
					t.Fatal("discovery target or credential leak")
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
			})}}
			out := runner.checkOAuthCompatibility(t.Context(), cfg, "revision")
			if out.State != tc.state || len(out.Checks) > api.MCPCatalogOAuthMaxChecks {
				t.Fatalf("check state: %s want %s", out.State, tc.state)
			}
		})
	}
}
func TestOAuthProviderMetadataRejectsUnsafeResponses(t *testing.T) {
	cfg := &api.MCPCatalogObserverOAuthConfig{Guided: true, Issuer: "https://provider.example", DiscoveryURL: "https://provider.example/metadata", AuthorizationEndpoint: "https://provider.example/authorize", TokenEndpoint: "https://provider.example/token", ClientID: "reader"}
	for _, tc := range []struct {
		body string
		code int
	}{{"PRIVATE_PROVIDER_ERROR", 500}, {strings.Repeat("X", api.MCPCatalogOAuthMaxResponseBytes+1), 200}, {`{}`, 302}} {
		runner := ObserverRunner{Client: &http.Client{Transport: oauthRoundTripper(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: tc.code, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
		})}}
		out := runner.checkOAuthCompatibility(t.Context(), cfg, "revision")
		data, _ := json.Marshal(out)
		if out.State != "unavailable" || strings.Contains(string(data), "PRIVATE") {
			t.Fatal("unsafe metadata leaked or accepted")
		}
	}
	if matchesOAuthCallbackPort("http://127.0.0.1:123/callback", 456) || !matchesOAuthCallbackPort("http://127.0.0.1:123/callback", 123) {
		t.Fatal("fixed callback port not bound")
	}
	_, e := oauthTokenRequest(t.Context(), &api.MCPCatalogObserverOAuthConfig{TokenEndpoint: "https://provider.example/token", ClientID: "client", TokenAuthMethod: "client_secret_post"}, url.Values{}, "")
	if e == nil {
		t.Fatal("confidential client fell back to public")
	}
}
