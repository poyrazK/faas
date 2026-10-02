package mcphosting

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConfigRequiresExplicitSafeProfile(t *testing.T) {
	valid := Config{Version: 1, Endpoint: "/mcp", Transport: "streamable-http", Mode: "stateless", Legacy: true, AllowedOrigins: []string{}, Auth: AuthConfig{Mode: "open"}}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*Config)
	}{
		{"missing auth", func(c *Config) { c.Auth.Mode = "" }},
		{"stdio", func(c *Config) { c.Transport = "stdio" }},
		{"stateful", func(c *Config) { c.Mode = "stateful" }},
		{"redirect path", func(c *Config) { c.Endpoint = "//evil.example" }},
		{"query", func(c *Config) { c.Endpoint = "/mcp?token=x" }},
		{"control", func(c *Config) { c.Endpoint = "/mcp\t" }},
		{"space", func(c *Config) { c.Endpoint = "/mcp path" }},
		{"route parameter", func(c *Config) { c.Endpoint = "/mcp/:tenant" }},
		{"dot segment", func(c *Config) { c.Endpoint = "/mcp/../healthz" }},
		{"encoded segment", func(c *Config) { c.Endpoint = "/mcp/%2e%2e" }},
		{"bad origin", func(c *Config) { c.AllowedOrigins = []string{"https://example.com/path"} }},
		{"open issuer", func(c *Config) { c.Auth.Issuer = "https://issuer.example" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := valid
			tc.change(&c)
			if err := c.Validate(); err == nil {
				t.Fatal("accepted invalid profile")
			}
		})
	}
	valid.Auth = AuthConfig{Mode: "external-oauth", Issuer: "https://issuer.example", JWKSURL: "https://issuer.example/jwks", Resource: "https://app.example/mcp", Scopes: []string{"mcp:tools"}}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	valid.Auth.Resource = "https://app.example/other"
	if err := valid.Validate(); err == nil {
		t.Fatal("accepted different resource endpoint")
	}
	valid.Auth.Resource = "https://app.example/mcp"
	body, _ := json.Marshal(valid)
	for _, suffix := range []string{` {}`, strings.Repeat(" ", 64<<10)} {
		if _, err := Decode(strings.NewReader(string(body) + suffix)); err == nil {
			t.Fatal("accepted oversized or trailing config")
		}
	}
	if _, err := Decode(strings.NewReader(strings.Replace(string(body), `"version":1`, `"version":1,"typo":true`, 1))); err == nil {
		t.Fatal("accepted unknown field")
	}
}

func TestConfigToolScopes(t *testing.T) {
	c := Config{Version: 1, Endpoint: "/mcp", Transport: "streamable-http", Mode: "stateless", AllowedOrigins: []string{}, Auth: AuthConfig{Mode: "open"}}
	for _, tc := range []struct {
		name, policy string
		valid        bool
	}{
		{"empty denies all", `{}`, true},
		{"explicit public tool", `{"greet":[]}`, true},
		{"null policy", `null`, false},
		{"array policy", `[]`, false},
		{"null scopes", `{"greet":null}`, false},
		{"string scopes", `{"greet":"read"}`, false},
		{"empty name", `{"":[]}`, false},
		{"control name", `{"bad\u0000name":[]}`, false},
		{"open scoped tool", `{"greet":["read"]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(c)
			body = []byte(strings.Replace(string(body), `"mode":"open"`, `"mode":"open","tool_scopes":`+tc.policy, 1))
			decoded, err := Decode(strings.NewReader(string(body)))
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
			if err != nil {
				return
			}
			encoded, err := json.Marshal(decoded)
			if err != nil || !strings.Contains(string(encoded), `"tool_scopes":`+tc.policy) {
				t.Fatalf("policy lost on serialization: %s err=%v", encoded, err)
			}
		})
	}
	c.Auth = AuthConfig{Mode: "external-oauth", Issuer: "https://issuer.example", JWKSURL: "https://issuer.example/jwks", Resource: "https://app.example/mcp", Scopes: []string{"mcp:tools"}}
	for _, scopes := range [][]string{{}, {"files:read", "files:write"}} {
		c.Auth.ToolScopes = ToolScopePolicy{"read": scopes}
		if err := c.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, scopes := range [][]string{nil, {""}, {"two words"}, {"quote\""}, {"slash\\"}, {"nonascii:é"}} {
		c.Auth.ToolScopes = ToolScopePolicy{"read": scopes}
		if err := c.Validate(); err == nil {
			t.Fatalf("accepted invalid scopes %q", scopes)
		}
	}
}
