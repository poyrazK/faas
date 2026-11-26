package main_test

// adr: 20261123192254 — oauth-proxy with an external OpenID Connect issuer,
// and keys scoped to named tools.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
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
	main.SetMCPOIDCHTTPClient(t, srv.Client())

	issuer = srv.URL + "/tenant"
	documents["/tenant/.well-known/openid-configuration"] = `{"issuer":"ISSUER","jwks_uri":"ISSUER/keys"}`
	got, err := main.MCPDiscoverJWKS(context.Background(), issuer)
	if err != nil || got != issuer+"/keys" {
		t.Fatalf("OpenID discovery = %q, %v", got, err)
	}

	// RFC 8414 metadata sits before the issuer's path.
	delete(documents, "/tenant/.well-known/openid-configuration")
	documents["/.well-known/oauth-authorization-server/tenant"] = `{"issuer":"ISSUER","jwks_uri":"ISSUER/as-keys"}`
	if got, err = main.MCPDiscoverJWKS(context.Background(), issuer); err != nil || got != issuer+"/as-keys" {
		t.Fatalf("authorization-server metadata = %q, %v", got, err)
	}

	documents["/tenant/.well-known/openid-configuration"] = `{"issuer":"https://other.example","jwks_uri":"ISSUER/keys"}`
	delete(documents, "/.well-known/oauth-authorization-server/tenant")
	if _, err = main.MCPDiscoverJWKS(context.Background(), issuer); err == nil || !strings.Contains(err.Error(), "names issuer") {
		t.Fatalf("a document naming another issuer must be refused: %v", err)
	}
	documents["/tenant/.well-known/openid-configuration"] = `{"issuer":"ISSUER","jwks_uri":"http://plain.example/keys"}`
	if _, err = main.MCPDiscoverJWKS(context.Background(), issuer); err == nil || !strings.Contains(err.Error(), "HTTPS jwks_uri") {
		t.Fatalf("a plain-HTTP key set must be refused: %v", err)
	}
	if _, err = main.MCPDiscoverJWKS(context.Background(), "http://idp.example"); err == nil {
		t.Fatal("a plain-HTTP issuer must be refused")
	}
}

func TestMCPAuthEnableConfigExternalIssuer(t *testing.T) {
	cfg := mcphosting.Config{Version: 1, Endpoint: "/mcp", Transport: "streamable-http", Mode: "stateless", AllowedOrigins: []string{},
		Auth: mcphosting.AuthConfig{Mode: mcphosting.AuthExternalOAuth, Issuer: "https://idp.example/", JWKSURL: "https://idp.example/jwks",
			Resource: "https://tools.example/mcp", Scopes: []string{"mcp:tools"}, ToolScopes: mcphosting.ScopePolicy{"search": {"mcp:tools"}}}}
	const jwks = "https://idp.example/.well-known/jwks.json"
	if err := main.MCPAuthEnableExternalIssuer(&cfg, mcphosting.AuthOAuthProxy, "https://tools.example/mcp", "https://idp.example/", jwks, "api://tools", ""); err != nil {
		t.Fatal(err)
	}
	if !cfg.Auth.ExternalIssuer() || cfg.Auth.Audience != "api://tools" || len(cfg.Auth.ToolScopes) != 1 || cfg.Auth.Scopes[0] != "mcp:tools" {
		t.Fatalf("auth = %+v, want the external issuer with the reviewed scopes kept", cfg.Auth)
	}
	action := main.McpExpectedGatewayAction(cfg, []string{"ES256"}, "https://api.example")
	if action.JWKSURL != jwks || action.Audience[0] != "api://tools" || action.MCP.Audience != "api://tools" || action.Validate() != nil || len(action.Algorithms) != 2 || action.Issuer != "https://idp.example/" {
		t.Fatalf("gateway action = %+v", action)
	}
	next := strings.Join(main.MCPAuthNext(cfg, "tools"), "\n")
	if !strings.Contains(next, `aud "api://tools"`) {
		t.Fatalf("next steps = %s", next)
	}
}

func TestMCPToolKeyScopes(t *testing.T) {
	dir := t.TempDir()
	write := func(auth string) {
		t.Helper()
		body := `{"version":1,"endpoint":"/mcp","transport":"streamable-http","mode":"stateless","allowed_origins":[],"auth":` + auth + `}`
		if err := os.WriteFile(filepath.Join(dir, mcphosting.ConfigFile), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"mode":"api-key","resource":"https://tools.example/mcp","scopes":["tools"],
"tool_scopes":{"search":[],"delete":["admin"],"export":["admin","export"]}}`)
	for _, tc := range []struct {
		tools []string
		want  string
		err   string
	}{
		{tools: []string{"search"}, want: "tools"},
		{tools: []string{"delete", "export"}, want: "admin,export,tools"},
		{tools: []string{"missing"}, err: `tool "missing" is not in auth.tool_scopes`},
		// export's scopes include admin, which also unlocks delete.
		{tools: []string{"export"}, err: "would also let the key call delete"},
	} {
		got, err := main.MCPToolKeyScopes(dir, tc.tools)
		if tc.err != "" {
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Fatalf("%v: err = %v, want %q", tc.tools, err, tc.err)
			}
			continue
		}
		if err != nil || strings.Join(got, ",") != tc.want {
			t.Fatalf("%v = %v, %v; want %s", tc.tools, got, err, tc.want)
		}
	}
	write(`{"mode":"open","tool_scopes":{"search":[]}}`)
	if _, err := main.MCPToolKeyScopes(dir, []string{"search"}); err == nil || !strings.Contains(err.Error(), "not api-key") {
		t.Fatalf("an open server must be refused: %v", err)
	}
}
