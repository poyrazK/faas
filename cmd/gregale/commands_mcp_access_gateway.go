package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

func mcpAccessRuleOverlaps(rule api.EdgeRuleResponse, resource *url.URL) bool {
	return api.MCPAccessRuleOverlaps(rule, resource)
}

func mcpAccessRuleActive(rule api.EdgeRuleResponse, now time.Time) bool {
	return rule.Enabled && !rule.Expired && (rule.ExpiresAt == nil || rule.ExpiresAt.After(now)) && rule.Mode != "log"
}

func auditMCPGatewayRules(appID string, cfg mcphosting.Config, options mcpAccessAuditOptions, rules []api.EdgeRuleResponse, now time.Time, checks []mcphosting.Check) (string, []mcphosting.Check) {
	resource, err := url.Parse(cfg.Auth.Resource)
	if err != nil || resource.Host == "" {
		return "", append(checks, mcphosting.Check{Name: "gateway_resource", Status: "failed", Detail: "Invalid reviewed canonical resource."})
	}
	active := []api.EdgeRuleResponse{}
	for _, rule := range rules {
		if mcpAccessRuleOverlaps(rule, resource) && mcpAccessRuleActive(rule, now) {
			active = append(active, rule)
		}
	}
	if options.AuthPlacement == "guest-only" {
		state := "passed"
		if len(active) > 0 {
			state = "failed"
		}
		return "", append(checks, mcphosting.Check{Name: "guest_only_gateway", Status: state, Detail: "Guest-only placement requires no active gateway JWT rules overlapping the MCP endpoint or metadata paths."})
	}
	// A managed rule is bound to this app and unconditionally covers the canonical
	// host and every path/method, as created by mcp policy.
	managed := []api.EdgeRuleResponse{}
	for _, rule := range active {
		if rule.AppID == appID && strings.EqualFold(rule.MatchHost, resource.Host) && rule.MatchPath == "/**" && len(rule.MatchMethods) == 0 && len(rule.MatchHeaders) == 0 && rule.Match == nil && (rule.Mode == "" || rule.Mode == "enforce") {
			managed = append(managed, rule)
		}
	}
	if len(managed) != 1 {
		return "", append(checks, mcphosting.Check{Name: "gateway_policy_selection", Status: "failed", Detail: "Require exactly one active unconditional app-bound JWT rule on the canonical host and /**. Missing, disabled, expired, log-only or conditional policies cannot qualify."})
	}
	selected := managed[0]
	state := "passed"
	if selected.ExpiresAt != nil {
		state = "failed"
	}
	checks = append(checks, mcphosting.Check{Name: "gateway_policy_expiration", Status: state, Detail: "The reviewed source policy requires a non-expiring gateway rule."})
	state = "passed"
	for _, rule := range active {
		if rule.ID != selected.ID && rule.Priority <= selected.Priority {
			state = "failed"
		}
	}
	checks = append(checks, mcphosting.Check{Name: "gateway_shadowing", Status: state, Detail: "No overlapping JWT rule may have earlier or equal priority; conditional rules can shadow a subset of client requests."})
	var actual api.EdgeRuleJWTAction
	action := selected.Action
	// The API listing uses the state action's kind-tagged union; accept the
	// flat action used in create/update requests as well.
	var tagged map[string]json.RawMessage
	if json.Unmarshal(action, &tagged) == nil && tagged["jwt"] != nil {
		kindOK := len(tagged) == 1 || (len(tagged) == 2 && string(tagged["kind"]) == `"jwt"`)
		if !kindOK {
			return selected.ID, append(checks, mcphosting.Check{Name: "gateway_policy_shape", Status: "failed", Detail: "JWT policy contains unexpected action kinds."})
		}
		action = tagged["jwt"]
	}
	decoder := json.NewDecoder(bytes.NewReader(action))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&actual) != nil || decoder.Decode(new(any)) != io.EOF || actual.Validate() != nil || actual.MCP == nil {
		return selected.ID, append(checks, mcphosting.Check{Name: "gateway_policy_shape", Status: "failed", Detail: "Managed JWT action is invalid, unsupported, or missing the MCP resource policy."})
	}
	expected := mcpExpectedGatewayAction(cfg, options.Algorithms)
	if expected.Validate() != nil {
		return selected.ID, append(checks, mcphosting.Check{Name: "source_gateway_contract", Status: "failed", Detail: "Source configuration cannot be represented by the gateway MCP policy contract."})
	}
	comparisons := []struct {
		name             string
		actual, expected any
	}{
		{"issuer", actual.Issuer, expected.Issuer}, {"audience", mcpAuditSet(actual.Audience), mcpAuditSet(expected.Audience)},
		{"jwks_url", actual.JWKSURL, expected.JWKSURL}, {"algorithms", mcpAuditSet(actual.Algorithms), mcpAuditSet(expected.Algorithms)},
		{"resource", actual.MCP.Resource, expected.MCP.Resource}, {"scopes", mcpAuditSet(actual.MCP.Scopes), mcpAuditSet(expected.MCP.Scopes)},
		{"allowed_origins", mcpAuditSet(actual.MCP.AllowedOrigins), mcpAuditSet(expected.MCP.AllowedOrigins)},
		{"tool_scopes", mcpAuditScopeMap(actual.MCP.ToolScopes), mcpAuditScopeMap(expected.MCP.ToolScopes)},
		{"resource_scopes", mcpAuditScopeMap(actual.MCP.ResourceScopes), mcpAuditScopeMap(expected.MCP.ResourceScopes)},
		{"prompt_scopes", mcpAuditScopeMap(actual.MCP.PromptScopes), mcpAuditScopeMap(expected.MCP.PromptScopes)},
	}
	for _, comparison := range comparisons {
		state = "passed"
		if !reflect.DeepEqual(comparison.actual, comparison.expected) {
			state = "failed"
		}
		checks = append(checks, mcphosting.Check{Name: "gateway_" + comparison.name, Status: state, Detail: "Stored gateway field must match the reviewed source policy and audit options."})
	}
	state = "passed"
	if len(actual.RequiredClaims) > 0 || actual.PlatformTenantExternalRefClaim != "" {
		state = "failed"
	}
	checks = append(checks, mcphosting.Check{Name: "gateway_extra_claim_constraints", Status: state, Detail: "Additional claim or tenant bindings require review outside the source MCP policy contract. Expiration enforcement may be strengthened independently."})
	return selected.ID, checks
}

func mcpExpectedGatewayAction(cfg mcphosting.Config, algorithms []string) api.EdgeRuleJWTAction {
	return api.EdgeRuleJWTAction{Issuer: cfg.Auth.Issuer, Audience: []string{cfg.Auth.Resource}, JWKSURL: cfg.Auth.JWKSURL, Algorithms: algorithms, MCP: &api.MCPResourcePolicy{Resource: cfg.Auth.Resource, Scopes: cfg.Auth.Scopes, AllowedOrigins: cfg.AllowedOrigins, ToolScopes: cfg.Auth.ToolScopes, ResourceScopes: cfg.Auth.ResourceScopes, PromptScopes: cfg.Auth.PromptScopes}}
}

func mcpAuditSet(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	result := slices.Clone(values)
	slices.Sort(result)
	return slices.Compact(result)
}
func mcpAuditScopeMap(policy map[string][]string) map[string][]string {
	if policy == nil {
		return nil
	}
	result := make(map[string][]string, len(policy))
	for entry, scopes := range policy {
		result[entry] = mcpAuditSet(scopes)
	}
	return result
}
