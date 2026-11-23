package main_test

// adr: 20261122194324 — api-key and oauth-proxy MCP access from the CLI.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

func gatewayModeSource() mcphosting.Config {
	return mcphosting.Config{Version: 1, Endpoint: "/mcp", Transport: "streamable-http", Mode: "stateless", AllowedOrigins: []string{},
		Auth: mcphosting.AuthConfig{Mode: mcphosting.AuthOpen, ToolScopes: mcphosting.ScopePolicy{"search": {}}}}
}

func TestMCPAuthEnableWritesGatewayModes(t *testing.T) {
	cfg := gatewayModeSource()
	if err := main.MCPAuthEnableConfig(&cfg, mcphosting.AuthAPIKey, "https://tools.example/mcp", "", ""); err != nil {
		t.Fatal(err)
	}
	if cfg.Auth.Mode != mcphosting.AuthAPIKey || cfg.Auth.Resource != "https://tools.example/mcp" || strings.Join(cfg.Auth.Scopes, ",") != "tools" || cfg.Auth.ToolScopes == nil {
		t.Fatalf("api-key auth = %+v", cfg.Auth)
	}
	action := main.McpExpectedGatewayAction(cfg, []string{"RS256"}, "https://api.example/")
	if action.Issuer != "https://api.example"+api.MCPConsumerKeyIssuerPath || action.JWKSURL != "https://api.example"+api.MCPConsumerKeyJWKSPath ||
		strings.Join(action.Algorithms, ",") != "ES256" || !action.MCP.ConsumerKey() || action.Validate() != nil {
		t.Fatalf("api-key action = %+v", action)
	}

	cfg = gatewayModeSource()
	if err := main.MCPAuthEnableConfig(&cfg, mcphosting.AuthOAuthProxy, "https://tools.example/mcp", "https://login.example/auth/v1", "openid"); err == nil {
		t.Fatal("oauth-proxy must refuse to drop reviewed tool scopes silently")
	}
	cfg.Auth.ToolScopes = nil
	if err := main.MCPAuthEnableConfig(&cfg, mcphosting.AuthOAuthProxy, "https://tools.example/mcp", "https://login.example/auth/v1", "openid"); err != nil {
		t.Fatal(err)
	}
	action = main.McpExpectedGatewayAction(cfg, []string{"RS256"}, "https://api.example")
	if action.Issuer != "https://login.example/auth/v1" || action.JWKSURL != "https://login.example/auth/v1/.well-known/jwks.json" || action.MCP.ConsumerKey() || action.Validate() != nil {
		t.Fatalf("oauth-proxy action = %+v", action)
	}

	enabled := true
	cfg = gatewayModeSource()
	cfg.Tasks = &mcphosting.TasksConfig{Enabled: &enabled}
	if err := main.MCPAuthEnableConfig(&cfg, mcphosting.AuthAPIKey, "https://tools.example/mcp", "", ""); err == nil || !strings.Contains(err.Error(), "per-caller ownership") {
		t.Fatalf("tasks server: %v", err)
	}
}

func TestMCPGatewayAuditMatchesAPIKeyPolicy(t *testing.T) {
	cfg := gatewayModeSource()
	if err := main.MCPAuthEnableConfig(&cfg, mcphosting.AuthAPIKey, "https://tools.example/mcp", "", "tools"); err != nil {
		t.Fatal(err)
	}
	stored := main.McpExpectedGatewayAction(cfg, nil, "https://api.example")
	body, _ := json.Marshal(stored)
	rule := api.EdgeRuleResponse{ID: "rule-1", AppID: "app-1", Kind: "jwt", Enabled: true, MatchHost: "tools.example", MatchPath: "/**", Action: body}
	options := main.McpAccessAuditOptions{AuthPlacement: "gateway", Algorithms: []string{"ES256"}}
	_, checks := main.AuditMCPGatewayRules("app-1", cfg, options, "https://api.example", []api.EdgeRuleResponse{rule}, time.Now(), nil)
	for _, check := range checks {
		if check.Status != "passed" {
			t.Fatalf("check %s = %s", check.Name, check.Status)
		}
	}
	// A JWT policy left from external-oauth does not satisfy an api-key source.
	stored.MCP.Credential = ""
	rule.Action, _ = json.Marshal(stored)
	_, checks = main.AuditMCPGatewayRules("app-1", cfg, options, "https://api.example", []api.EdgeRuleResponse{rule}, time.Now(), nil)
	failed := false
	for _, check := range checks {
		failed = failed || check.Name == "gateway_credential" && check.Status == "failed"
	}
	if !failed {
		t.Fatalf("credential drift not reported: %+v", checks)
	}
	// Guest-only placement can never protect a gateway-only mode.
	_, checks = main.AuditMCPGatewayRules("app-1", cfg, main.McpAccessAuditOptions{AuthPlacement: "guest-only"}, "https://api.example", nil, time.Now(), nil)
	if len(checks) != 1 || checks[0].Status != "failed" {
		t.Fatalf("guest-only checks = %+v", checks)
	}
}

func TestMCPGatewayPreviewIsHidden(t *testing.T) {
	t.Setenv("FAAS_MCP_GATEWAY_PREVIEW_ENABLED", "")
	for _, command := range main.VisibleCliCommands() {
		if command.Name != "mcp" {
			continue
		}
		for _, sub := range command.Subcommands {
			if sub.Name == "auth" || sub.Name == "replay" {
				t.Fatalf("mcp %s is visible while the preview is off", sub.Name)
			}
		}
	}
	// Hidden, the subcommand is unknown: it fails before reading any flag or
	// credential.
	if code, _, _ := runCLI(t, "mcp", "auth", "enable", "--mode", "api-key", "--app", "x"); code == 0 {
		t.Fatal("hidden command dispatched")
	}
}
