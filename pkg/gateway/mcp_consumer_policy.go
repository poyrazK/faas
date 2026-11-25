package gateway

import (
	"context"
	"math"
	"net/http"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
)

// applyMCPConsumerKeyPolicy enforces an api-key MCP policy
// (ADR-20261122194324). enforceConsumerAuth already verified the consumer
// key; this gate only reads the identity it stamped, so a missing identity
// means no key, an inactive key or an app without consumer auth.
func (h *Handler) applyMCPConsumerKeyPolicy(w http.ResponseWriter, r *http.Request, app App, rule *EdgeRuleJWTResolved) bool {
	p := rule.MCP
	identity, ok := authmw.ConsumerFromContext(r)
	if !ok {
		h.jwtEmit(r.Context(), "jwt", "failed", rule.ID, r.Host, nil, nil)
		return mcpPolicyReject(w, http.StatusUnauthorized, p, "Consumer key required")
	}
	granted := api.ConsumerKeyMCPScopes(identity.Scopes)
	if !api.MCPHasScopes(p.Scopes, granted) {
		return mcpPolicyReject(w, http.StatusForbidden, p, "Required endpoint scope is missing")
	}
	status, reason, call := mcpPolicyRequest(r, p, granted)
	if status != 0 {
		return mcpPolicyReject(w, status, p, reason)
	}
	if h.mcpToolLimited(w, r, app, rule, call.tool(), "consumer\x00"+identity.ID) {
		return true
	}
	h.applyMCPBilling(r, app, p, call)
	r.Header.Set(api.MCPGatewayAuthorizedHeader, rule.ID)
	h.jwtEmit(r.Context(), "jwt", "match", rule.ID, r.Host, nil, nil)
	return false
}

// mcpToolLimited applies the policy's tool limit for tool to caller. It
// reports true after answering 429. Buckets are per gateway process, like the
// local throttle mode (ADR-104).
func (h *Handler) mcpToolLimited(w http.ResponseWriter, r *http.Request, app App, rule *EdgeRuleJWTResolved, tool, caller string) bool {
	limit, ok := rule.MCP.ToolLimits[tool]
	if tool == "" || !ok || h.routeConsumerLimiter == nil {
		return false
	}
	rps := float64(limit.Requests) / float64(limit.WindowSeconds)
	bucket := app.ID + "\x00" + rule.ID + "\x00mcp-tool\x00" + tool
	if h.routeConsumerLimiter.AllowWithConsumerKey(bucket, caller, rps, float64(limit.Requests), api.ThrottleMaxKeysPerRuleDefault) {
		return false
	}
	w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(1/rps))))
	w.Header().Set("Cache-Control", "no-store")
	api.WriteProblem(w, api.NewProblem(http.StatusTooManyRequests, api.CodeMCPToolRateLimited, "MCP tool rate limited",
		"This caller reached the tool's limit of "+strconv.Itoa(limit.Requests)+" calls per "+strconv.Itoa(limit.WindowSeconds)+" s"))
	return true
}

// mcpConsumerKeyEndpoint reports whether r targets the endpoint or metadata
// path of a consumer-key MCP policy owned by app. A key's "mcp:" scopes stand
// in for the ADR-120 method check only there, so they reach nothing else.
func (h *Handler) mcpConsumerKeyEndpoint(r *http.Request, app App) bool {
	if h.edgeRules == nil {
		return false
	}
	match := func(host string) bool {
		for _, rule := range matchEdgeGates(r, func(ctx context.Context, path string) *EdgeRuleJWTResolved {
			return h.edgeRules.MatchJWT(ctx, host, path, r.Method)
		}) {
			if rule == nil || rule.Unavailable || !rule.MCP.ConsumerKey() || rule.AccountID != app.AccountID || rule.AppID != app.ID || rule.MCP.Validate() != nil {
				continue
			}
			if r.URL.Path == rule.MCP.EndpointPath() || r.URL.Path == rule.MCP.MetadataPath() {
				return true
			}
		}
		return false
	}
	if match(hostname(r.Host)) {
		return true
	}
	return app.CanonicalHost != "" && app.CanonicalHost != hostname(r.Host) && match(app.CanonicalHost)
}
