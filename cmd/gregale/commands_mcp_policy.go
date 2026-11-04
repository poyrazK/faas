package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
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
	a := mcpExpectedGatewayAction(cfg, algorithms)
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
		action, err := decodeMCPGatewayAction(rule.Action)
		if err != nil || action.MCP == nil || rule.MatchHost != resource.Host || rule.MatchPath != "/**" || len(rule.MatchMethods) != 0 || len(rule.MatchHeaders) != 0 || existing != nil {
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
	actual, err := decodeMCPGatewayAction(rule.Action)
	// Normalize omitted empty slices through the wire representation, while
	// retaining explicit empty scope maps, whose deny-all semantics matter.
	encoded, _ := json.Marshal(expected)
	var normalized api.EdgeRuleJWTAction
	_ = json.Unmarshal(encoded, &normalized)
	if err != nil || actual.RequiresExp() != normalized.RequiresExp() {
		return fmt.Errorf("control plane did not persist the requested MCP policy on rule %s; upgrade apid and gateway before activation", rule.ID)
	}
	actual.RequireExp, normalized.RequireExp = nil, nil
	if !reflect.DeepEqual(actual, normalized) {
		return fmt.Errorf("control plane did not persist the requested MCP policy on rule %s; upgrade apid and gateway before activation", rule.ID)
	}
	return nil
}

// Responses contain the state's tagged union; requests use the flat JWT body.
// Reject other action kinds and unknown fields rather than dropping constraints.
func decodeMCPGatewayAction(body json.RawMessage) (api.EdgeRuleJWTAction, error) {
	var action api.EdgeRuleJWTAction
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || fields == nil {
		return action, errors.New("invalid MCP gateway action")
	}
	if nested, present := fields["jwt"]; present {
		var kind string
		if raw, ok := fields["kind"]; ok {
			if json.Unmarshal(raw, &kind) != nil || kind != "jwt" || len(fields) != 2 {
				return action, errors.New("invalid MCP gateway action kind")
			}
		} else if len(fields) != 1 {
			return action, errors.New("invalid MCP gateway action envelope")
		}
		body = nested
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&action) != nil || decoder.Decode(new(any)) != io.EOF || bytes.Equal(bytes.TrimSpace(body), []byte("null")) {
		return action, errors.New("invalid MCP gateway action")
	}
	return action, nil
}
