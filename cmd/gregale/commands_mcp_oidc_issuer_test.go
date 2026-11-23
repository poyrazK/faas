package main

// adr: 20261123192254 — oauth-proxy with an external OpenID Connect issuer.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/mcphosting"
)

func TestMCPDiscoverJWKS(t *testing.T) {
	var issuer string
	documents := map[string]string{}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := documents[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(strings.ReplaceAll(body, "ISSUER", issuer)))
	}))
	defer srv.Close()
	previous := mcpOIDCHTTPClient
	mcpOIDCHTTPClient = srv.Client()
	defer func() { mcpOIDCHTTPClient = previous }()

	issuer = srv.URL + "/tenant"
	documents["/tenant/.well-known/openid-configuration"] = `{"issuer":"ISSUER","jwks_uri":"ISSUER/keys"}`
	got, err := mcpDiscoverJWKS(context.Background(), issuer)
	if err != nil || got != issuer+"/keys" {
		t.Fatalf("OpenID discovery = %q, %v", got, err)
	}

	// RFC 8414 metadata sits before the issuer's path.
	delete(documents, "/tenant/.well-known/openid-configuration")
	documents["/.well-known/oauth-authorization-server/tenant"] = `{"issuer":"ISSUER","jwks_uri":"ISSUER/as-keys"}`
	if got, err = mcpDiscoverJWKS(context.Background(), issuer); err != nil || got != issuer+"/as-keys" {
		t.Fatalf("authorization-server metadata = %q, %v", got, err)
	}

	documents["/tenant/.well-known/openid-configuration"] = `{"issuer":"https://other.example","jwks_uri":"ISSUER/keys"}`
	delete(documents, "/.well-known/oauth-authorization-server/tenant")
	if _, err = mcpDiscoverJWKS(context.Background(), issuer); err == nil || !strings.Contains(err.Error(), "names issuer") {
		t.Fatalf("a document naming another issuer must be refused: %v", err)
	}
	documents["/tenant/.well-known/openid-configuration"] = `{"issuer":"ISSUER","jwks_uri":"http://plain.example/keys"}`
	if _, err = mcpDiscoverJWKS(context.Background(), issuer); err == nil || !strings.Contains(err.Error(), "HTTPS jwks_uri") {
		t.Fatalf("a plain-HTTP key set must be refused: %v", err)
	}
	if _, err = mcpDiscoverJWKS(context.Background(), "http://idp.example"); err == nil {
		t.Fatal("a plain-HTTP issuer must be refused")
	}
}

func TestMCPAuthEnableConfigExternalIssuer(t *testing.T) {
	cfg := mcphosting.Config{Version: 1, Endpoint: "/mcp", Transport: "streamable-http", Mode: "stateless", AllowedOrigins: []string{},
		Auth: mcphosting.AuthConfig{Mode: mcphosting.AuthExternalOAuth, Issuer: "https://idp.example/", JWKSURL: "https://idp.example/jwks",
			Resource: "https://tools.example/mcp", Scopes: []string{"mcp:tools"}, ToolScopes: mcphosting.ScopePolicy{"search": {"mcp:tools"}}}}
	issuer := mcpAuthIssuer{url: "https://idp.example/", jwksURL: "https://idp.example/.well-known/jwks.json", audience: "api://tools"}
	if err := mcpAuthEnableConfig(&cfg, mcphosting.AuthOAuthProxy, "https://tools.example/mcp", issuer, ""); err != nil {
		t.Fatal(err)
	}
	if !cfg.Auth.ExternalIssuer() || cfg.Auth.Audience != "api://tools" || len(cfg.Auth.ToolScopes) != 1 || cfg.Auth.Scopes[0] != "mcp:tools" {
		t.Fatalf("auth = %+v, want the external issuer with the reviewed scopes kept", cfg.Auth)
	}
	action := mcpExpectedGatewayAction(cfg, []string{"ES256"}, "https://api.example")
	if action.JWKSURL != issuer.jwksURL || action.Audience[0] != "api://tools" || len(action.Algorithms) != 2 || action.Issuer != issuer.url {
		t.Fatalf("gateway action = %+v", action)
	}
	next := strings.Join(mcpAuthNext(cfg, "tools"), "\n")
	if !strings.Contains(next, `aud "api://tools"`) {
		t.Fatalf("next steps = %s", next)
	}
}
