package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

// mcpVerificationCredential names what --token-env must hold for a mode.
func mcpVerificationCredential(mode string) string {
	switch mode {
	case mcphosting.AuthAPIKey:
		return "consumer key (ck_…) with the server's mcp: scopes"
	case mcphosting.AuthOAuthProxy:
		return "Gregale Auth client access token for this server"
	default:
		return "client access token"
	}
}

// requireMCPGatewayPolicy refuses to deploy an api-key or oauth-proxy server
// until its gateway policy is stored and matches the source: in these modes
// the server itself admits every request, so without the policy it would
// serve anyone (ADR-20261122194324).
func requireMCPGatewayPolicy(ctx context.Context, c *Client, slug string, cfg mcphosting.Config) error {
	if !cfg.Auth.GatewayOnly() {
		return nil
	}
	enable := fmt.Sprintf("run `gregale mcp auth enable --mode %s --app %s` first", cfg.Auth.Mode, slug)
	app, err := c.GetApp(ctx, slug)
	if err != nil {
		var problem *api.Problem
		if errors.As(err, &problem) && problem.Status == 404 {
			return fmt.Errorf("%s needs its gateway policy before the first deploy; create the app and %s", cfg.Auth.Mode, enable)
		}
		return err
	}
	endpoint, err := cfg.URL(canonicalAppURL(app))
	if err != nil {
		return err
	}
	if endpoint != cfg.Auth.Resource {
		return fmt.Errorf("auth.resource must match the canonical endpoint %s", endpoint)
	}
	rules, err := c.ListCoveringEdgeRulesForApp(ctx, slug)
	if err != nil {
		return fmt.Errorf("could not read the app's gateway rules: %w", err)
	}
	_, checks := auditMCPGatewayRules(app.ID, cfg, mcpAccessAuditOptions{AuthPlacement: "gateway", Algorithms: []string{"ES256"}}, c.BaseURL(), rules, time.Now(), nil)
	for _, check := range checks {
		if check.Status != "passed" {
			return fmt.Errorf("stored gateway policy does not match the source (%s); %s", check.Name, enable)
		}
	}
	return nil
}
