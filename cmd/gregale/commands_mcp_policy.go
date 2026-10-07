package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"reflect"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

func cmdMCPPolicy(args []string) int {
	fs := newFlagSet("mcp-policy", flag.ContinueOnError)
	path := fs.String("path", ".", "MCP source directory")
	slug := fs.String("name", "", "app slug receiving the gateway resource policy")
	algorithms := fs.String("algorithms", "RS256,ES256", "comma-separated JWT signature algorithms")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 || *slug == "" {
		return printErr("MCP policy", errors.New("--name is required"))
	}
	cfg, err := mcphosting.Load(*path)
	if err != nil {
		return printErr("MCP configuration", err)
	}
	if cfg.Auth.Mode != "external-oauth" {
		return printErr("MCP policy", errors.New("gateway resource policy requires external-oauth"))
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	rule, err := applyMCPGatewayPolicy(context.Background(), c, *slug, cfg, strings.Split(*algorithms, ","))
	if err != nil {
		return printErr("MCP gateway policy", err)
	}
	return jsonOut(writeJSON(rule))
}

func applyMCPGatewayPolicy(ctx context.Context, c *Client, slug string, cfg mcphosting.Config, algorithms []string) (api.EdgeRuleResponse, error) {
	app, err := c.GetApp(ctx, slug)
	if err != nil {
		return api.EdgeRuleResponse{}, err
	}
	endpoint, err := cfg.URL(canonicalAppURL(app))
	if err != nil {
		return api.EdgeRuleResponse{}, err
	}
	if cfg.Auth.Resource != endpoint {
		return api.EdgeRuleResponse{}, errors.New("MCP resource must match the app's canonical endpoint")
	}
	resource, _ := url.Parse(endpoint)
	a := api.EdgeRuleJWTAction{Issuer: cfg.Auth.Issuer, Audience: []string{endpoint}, JWKSURL: cfg.Auth.JWKSURL, Algorithms: algorithms,
		MCP: &api.MCPResourcePolicy{Resource: endpoint, Scopes: cfg.Auth.Scopes, AllowedOrigins: cfg.AllowedOrigins, ToolScopes: cfg.Auth.ToolScopes, ResourceScopes: cfg.Auth.ResourceScopes, PromptScopes: cfg.Auth.PromptScopes}}
	if problem := a.Validate(); problem != nil {
		return api.EdgeRuleResponse{}, errors.New(problem.Detail)
	}
	// A second JWT rule could shadow this policy. Require deliberate resolution
	// through edge-rules rather than silently replacing unrelated authentication.
	rules, err := c.ListEdgeRulesForApp(ctx, slug)
	if err != nil {
		return api.EdgeRuleResponse{}, err
	}
	var existing *api.EdgeRuleResponse
	for i := range rules {
		rule := &rules[i]
		if rule.Kind != "jwt" || !rule.Enabled {
			continue
		}
		var action api.EdgeRuleJWTAction
		if json.Unmarshal(rule.Action, &action) != nil || action.MCP == nil || rule.MatchHost != resource.Host || rule.MatchPath != "/**" || len(rule.MatchMethods) != 0 || len(rule.MatchHeaders) != 0 || existing != nil {
			return api.EdgeRuleResponse{}, errors.New("resolve existing JWT rules before applying an MCP gateway policy")
		}
		existing = rule
	}
	encoded, err := json.Marshal(a)
	if err != nil {
		return api.EdgeRuleResponse{}, err
	}
	body := json.RawMessage(encoded)
	if existing != nil {
		updated, err := c.UpdateEdgeRule(ctx, existing.ID, api.UpdateEdgeRuleRequest{Action: &body})
		if err != nil {
			return updated, err
		}
		return updated, verifyMCPPolicyEcho(updated, a)
	}
	priority := 0
	disabled := false
	staged, err := c.CreateEdgeRule(ctx, slug, api.CreateEdgeRuleRequest{MatchHost: resource.Host, MatchPath: "/**", Priority: &priority, Enabled: &disabled, Kind: "jwt", Action: body})
	if err != nil {
		return staged, err
	}
	if err := verifyMCPPolicyEcho(staged, a); err != nil {
		return staged, err
	}
	enabled := true
	return c.UpdateEdgeRule(ctx, staged.ID, api.UpdateEdgeRuleRequest{Enabled: &enabled})
}

func verifyMCPPolicyEcho(rule api.EdgeRuleResponse, expected api.EdgeRuleJWTAction) error {
	var actual api.EdgeRuleJWTAction
	// Normalize omitted empty slices through the wire representation, while
	// retaining explicit empty scope maps, whose deny-all semantics matter.
	encoded, _ := json.Marshal(expected)
	var normalized api.EdgeRuleJWTAction
	_ = json.Unmarshal(encoded, &normalized)
	if json.Unmarshal(rule.Action, &actual) != nil || !reflect.DeepEqual(actual, normalized) {
		return fmt.Errorf("control plane did not persist the requested MCP policy on rule %s; upgrade apid and gateway before activation", rule.ID)
	}
	return nil
}
