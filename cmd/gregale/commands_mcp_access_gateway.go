package main

import (
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

func auditMCPGatewayRules(appID string, cfg mcphosting.Config, options mcpAccessAuditOptions, apiBase string, rules []api.EdgeRuleResponse, now time.Time, checks []mcphosting.Check) (string, []mcphosting.Check) {
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
		if len(active) > 0 || cfg.Auth.GatewayOnly() {
			state = "failed"
		}
		return "", append(checks, mcphosting.Check{Name: "guest_only_gateway", Status: state, Detail: "Guest-only placement requires no active gateway JWT rules overlapping the MCP endpoint or metadata paths, and an auth mode the server verifies itself (api-key and oauth-proxy are gateway-only)."})
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
	actual, decodeErr := decodeMCPGatewayAction(selected.Action)
	if decodeErr != nil || actual.Validate() != nil || actual.MCP == nil {
		return selected.ID, append(checks, mcphosting.Check{Name: "gateway_policy_shape", Status: "failed", Detail: "Managed JWT action is invalid, unsupported, or missing the MCP resource policy."})
	}
	expected := mcpExpectedGatewayAction(cfg, options.Algorithms, apiBase)
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
		{"credential", actual.MCP.Credential, expected.MCP.Credential},
		{"audience", actual.MCP.Audience, expected.MCP.Audience},
		{"tool_limits", mcpAuditToolLimits(actual.MCP.ToolLimits), mcpAuditToolLimits(expected.MCP.ToolLimits)},
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

// mcpExpectedGatewayAction is the gateway rule action a reviewed source
// configuration needs. apiBase is the platform API origin; api-key policies
// name its empty key set so gateways that predate consumer-key policies fail
// closed (ADR-20261122194324). Gateway-only modes fix the algorithm to ES256.
func mcpExpectedGatewayAction(cfg mcphosting.Config, algorithms []string, apiBase string) api.EdgeRuleJWTAction {
	issuer, jwks := cfg.Auth.Issuer, cfg.Auth.JWKSURL
	policy := &api.MCPResourcePolicy{Resource: cfg.Auth.Resource, Scopes: cfg.Auth.Scopes, AllowedOrigins: cfg.AllowedOrigins, ToolScopes: cfg.Auth.ToolScopes, ResourceScopes: cfg.Auth.ResourceScopes, PromptScopes: cfg.Auth.PromptScopes, ToolLimits: cfg.Auth.ToolLimits}
	switch cfg.Auth.Mode {
	case mcphosting.AuthAPIKey:
		base := strings.TrimSuffix(apiBase, "/")
		issuer, jwks, algorithms = base+api.MCPConsumerKeyIssuerPath, base+api.MCPConsumerKeyJWKSPath, []string{"ES256"}
		policy.Credential = api.MCPCredentialConsumerKey
	case mcphosting.AuthOAuthProxy:
		if cfg.Auth.ExternalIssuer() {
			// External OpenID Connect issuers sign with RSA or ECDSA keys
			// (ADR-20261123192254).
			algorithms = []string{"RS256", "ES256"}
			policy.Audience = cfg.Auth.Audience
		} else {
			jwks, algorithms = mcphosting.OAuthProxyJWKSURL(issuer), []string{"ES256"}
		}
	}
	return api.EdgeRuleJWTAction{Issuer: issuer, Audience: []string{cfg.Auth.TokenAudience()}, JWKSURL: jwks, Algorithms: algorithms, MCP: policy}
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

func mcpAuditToolLimits(limits map[string]api.MCPToolLimit) map[string]api.MCPToolLimit {
	if len(limits) == 0 {
		return nil
	}
	return limits
}
