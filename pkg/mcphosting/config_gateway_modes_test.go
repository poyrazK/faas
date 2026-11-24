package mcphosting

// adr: 20261122194324 — api-key and oauth-proxy access modes.

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func gatewayModeConfig(mode string) Config {
	c := Config{Version: 1, Endpoint: "/mcp", Transport: "streamable-http", Mode: "stateless", AllowedOrigins: []string{}, Auth: AuthConfig{Mode: mode, Resource: "https://tools.example/mcp"}}
	switch mode {
	case AuthAPIKey:
		c.Auth.Scopes = []string{"tools"}
	case AuthOAuthProxy:
		c.Auth.Issuer, c.Auth.Scopes = "https://login.example/auth/v1", []string{"openid"}
	}
	return c
}

func TestGatewayAuthModes(t *testing.T) {
	enabled := true
	for _, tc := range []struct {
		name, mode, want string
		change           func(*Config)
	}{
		{"api-key", AuthAPIKey, "", nil},
		{"api-key tool scopes and limits", AuthAPIKey, "", func(c *Config) {
			c.Auth.ToolScopes = ScopePolicy{"search": {"read"}}
			c.Auth.ToolLimits = map[string]api.MCPToolLimit{"search": {Requests: 10, WindowSeconds: 60}}
		}},
		{"api-key needs a scope", AuthAPIKey, "at least one scope", func(c *Config) { c.Auth.Scopes = nil }},
		{"api-key has no issuer", AuthAPIKey, "must not name an issuer", func(c *Config) { c.Auth.Issuer = "https://issuer.example" }},
		{"api-key resource is the endpoint", AuthAPIKey, "canonical MCP endpoint", func(c *Config) { c.Auth.Resource = "https://tools.example/other" }},
		{"api-key refuses tasks", AuthAPIKey, "per-caller ownership", func(c *Config) {
			c.Tasks = &TasksConfig{Enabled: &enabled, DatabaseURLEnv: "DATABASE_URL", OwnerKeyEnv: "MCP_TASK_OWNER_KEY", NamespaceEnv: "MCP_TASK_NAMESPACE"}
		}},
		{"oauth-proxy", AuthOAuthProxy, "", nil},
		{"oauth-proxy external issuer", AuthOAuthProxy, "", func(c *Config) {
			c.Auth.Issuer, c.Auth.JWKSURL, c.Auth.Scopes = "https://tenant.idp.example/", "https://tenant.idp.example/.well-known/jwks.json", []string{"mcp:tools"}
			c.Auth.ToolScopes, c.Auth.Audience = ScopePolicy{"search": {"mcp:tools"}}, "api://tools"
		}},
		{"external issuer jwks is https", AuthOAuthProxy, "auth.jwks_url", func(c *Config) {
			c.Auth.Issuer, c.Auth.JWKSURL = "https://tenant.idp.example/", "http://tenant.idp.example/jwks"
		}},
		{"external issuer needs a scope", AuthOAuthProxy, "external issuer requires at least one scope", func(c *Config) {
			c.Auth.Issuer, c.Auth.JWKSURL, c.Auth.Scopes = "https://tenant.idp.example/", "https://tenant.idp.example/jwks", nil
		}},
		{"audience only with an external issuer", AuthOAuthProxy, "auth.audience", func(c *Config) { c.Auth.Audience = "api://tools" }},
		{"audience has no whitespace", AuthOAuthProxy, "auth.audience", func(c *Config) {
			c.Auth.Issuer, c.Auth.JWKSURL, c.Auth.Audience = "https://tenant.idp.example/", "https://tenant.idp.example/jwks", "api tools"
		}},
		{"oauth-proxy needs a Gregale Auth issuer", AuthOAuthProxy, "Gregale Auth issuer", func(c *Config) { c.Auth.Issuer = "https://login.example/" }},
		{"oauth-proxy needs a scope", AuthOAuthProxy, "at least one scope", func(c *Config) { c.Auth.Scopes = nil }},
		{"oauth-proxy scopes are OpenID scopes", AuthOAuthProxy, "only the openid", func(c *Config) { c.Auth.Scopes = []string{"tools"} }},
		{"oauth-proxy has no scoped entries", AuthOAuthProxy, "scoped tool_scopes", func(c *Config) { c.Auth.ToolScopes = ScopePolicy{"search": {"read"}} }},
		{"tool limit bounds", AuthAPIKey, "tool_limits.search", func(c *Config) {
			c.Auth.ToolLimits = map[string]api.MCPToolLimit{"search": {Requests: 1, WindowSeconds: 7200}}
		}},
		{"open has no tool limits", AuthOpen, "tool limits", func(c *Config) {
			c.Auth = AuthConfig{Mode: AuthOpen, ToolLimits: map[string]api.MCPToolLimit{"search": {Requests: 1, WindowSeconds: 1}}}
		}},
		{"unknown mode", "basic", "open, external-oauth, api-key or oauth-proxy", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := gatewayModeConfig(tc.mode)
			if tc.change != nil {
				tc.change(&c)
			}
			err := c.Validate()
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("Validate() = %v, want %q", err, tc.want)
			}
		})
	}
	if got := OAuthProxyJWKSURL("https://login.example/auth/v1"); got != "https://login.example/auth/v1/.well-known/jwks.json" {
		t.Fatalf("OAuthProxyJWKSURL = %q", got)
	}
	external := AuthConfig{Mode: AuthOAuthProxy, Resource: "https://tools.example/mcp", JWKSURL: "https://idp.example/jwks"}
	if !external.ExternalIssuer() || external.TokenAudience() != "https://tools.example/mcp" {
		t.Fatalf("external issuer audience defaults to the resource: %q", external.TokenAudience())
	}
	external.Audience = "api://tools"
	if external.TokenAudience() != "api://tools" || gatewayModeConfig(AuthOAuthProxy).Auth.ExternalIssuer() {
		t.Fatal("audience override or Gregale Auth detection is wrong")
	}
}
