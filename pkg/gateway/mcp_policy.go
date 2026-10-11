package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func mcpPolicyReject(w http.ResponseWriter, status int, p *api.MCPResourcePolicy, reason string) bool {
	w.Header().Set("Cache-Control", "no-store")
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		challenge := fmt.Sprintf(`Bearer resource_metadata="%s"`, p.MetadataURL())
		if status == http.StatusForbidden {
			challenge += `, error="insufficient_scope"`
		}
		w.Header().Set("WWW-Authenticate", challenge)
	}
	api.WriteProblem(w, api.NewProblem(status, "mcp_access_denied", "MCP access denied", reason))
	return true
}

func mcpPolicyMetadata(w http.ResponseWriter, r *http.Request, rule *EdgeRuleJWTResolved) bool {
	p := rule.MCP
	if r.URL.Path != p.MetadataPath() {
		return false
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return true
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{"resource": p.Resource, "authorization_servers": []string{rule.Issuer}, "scopes_supported": p.Scopes, "bearer_methods_supported": []string{"header"}})
	return true
}

func (h *Handler) applyMCPResourcePolicy(w http.ResponseWriter, r *http.Request, app App, rule *EdgeRuleJWTResolved) bool {
	p := rule.MCP
	if err := p.Validate(); err != nil {
		h.rejectUnavailableEdgeRule(w, r, "jwt", rule.ID, "invalid_mcp_policy")
		return true
	}
	if r.URL.Path != p.EndpointPath() && r.URL.Path != p.MetadataPath() {
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" && !slices.Contains(p.AllowedOrigins, origin) {
		return mcpPolicyReject(w, http.StatusForbidden, p, "Origin is not allowed")
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Add("Vary", "Origin")
		w.Header().Set("Access-Control-Expose-Headers", "WWW-Authenticate, MCP-Session-Id, MCP-Protocol-Version")
	}
	if mcpPolicyMetadata(w, r, rule) {
		return true
	}
	if mcpPolicyPreflight(w, r) {
		return true
	}
	claims, handled := h.authenticateMCPResource(w, r, app, rule)
	if handled {
		return true
	}
	granted := strings.Fields(claims.Custom["scope"])
	if !api.MCPHasScopes(p.Scopes, granted) {
		return mcpPolicyReject(w, http.StatusForbidden, p, "Required endpoint scope is missing")
	}
	if status, reason := mcpPolicyRequest(r, p, granted); status != 0 {
		return mcpPolicyReject(w, status, p, reason)
	}
	authenticated := authenticatedFrom(r.Context())
	authenticated.JWTSubject, authenticated.JWTClaims = claims.Subject, claims.Custom
	if h.applyPlatformTenantJWTClaim(w, r, app, rule, claims, &authenticated) {
		return true
	}
	ctx, cancel := context.WithDeadline(withAuthenticated(r.Context(), authenticated), claims.Exp)
	context.AfterFunc(r.Context(), cancel)
	*r = *r.WithContext(ctx)
	h.jwtEmit(r.Context(), "jwt", "match", rule.ID, r.Host, nil, nil)
	return false
}

func mcpPolicyPreflight(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodOptions {
		return false
	}
	if r.Header.Get("Origin") != "" {
		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Accept, MCP-Protocol-Version, MCP-Session-Id, MCP-Method, MCP-Name")
	}
	w.WriteHeader(http.StatusNoContent)
	return true
}

func (h *Handler) authenticateMCPResource(w http.ResponseWriter, r *http.Request, app App, rule *EdgeRuleJWTResolved) (*JWTClaims, bool) {
	if h.jwtVerifier == nil {
		h.rejectUnavailableEdgeRule(w, r, "jwt", rule.ID, "jwt_verifier_not_configured")
		return nil, true
	}
	raw := bearerTokenFromHeader(r.Header.Get("Authorization"))
	if raw == "" {
		return nil, mcpPolicyReject(w, http.StatusUnauthorized, rule.MCP, "Bearer token required")
	}
	verifyRule := *rule
	verifyRule.ExtractClaims = append(append([]string{}, rule.ExtractClaims...), "scope")
	if rule.PlatformTenantExternalRefClaim != "" {
		verifyRule.ExtractClaims = append(verifyRule.ExtractClaims, rule.PlatformTenantExternalRefClaim)
	}
	if h.edgeRules != nil {
		if throttle := h.edgeRules.MatchThrottle(r.Context(), hostname(r.Host), r.URL.Path, r.Method); throttle != nil && throttle.AccountID == app.AccountID && throttle.KeyBy == api.ThrottleKeyByJWTClaim && throttle.JWTClaimName != "" {
			verifyRule.ExtractClaims = append(verifyRule.ExtractClaims, throttle.JWTClaimName)
		}
	}
	claims, err := h.verifyJWTWithDeadline(r.Context(), raw, &verifyRule)
	if err != nil || claims == nil || claims.Subject == "" || claims.Exp.IsZero() || !claims.Exp.After(time.Now()) {
		h.jwtEmit(r.Context(), "jwt", "failed", rule.ID, r.Host, nil, nil)
		return nil, mcpPolicyReject(w, http.StatusUnauthorized, rule.MCP, "Invalid or expired bearer token")
	}
	return claims, false
}

// Inspect the original JSON-RPC envelope and restore its exact bytes for the
// guest. Caller-supplied MCP-Method/MCP-Name headers cannot grant access.
func mcpPolicyRequest(r *http.Request, p *api.MCPResourcePolicy, scopes []string) (int, string) {
	if r.Method != http.MethodPost {
		return 0, ""
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, api.MCPPolicyMaxRequestBytes+1))
	if err != nil || len(body) > api.MCPPolicyMaxRequestBytes {
		return http.StatusRequestEntityTooLarge, "MCP request exceeds the gateway policy body limit"
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	var envelope map[string]json.RawMessage
	if json.Unmarshal(body, &envelope) != nil {
		return http.StatusBadRequest, "A single JSON-RPC method is required"
	}
	readString := func(fields map[string]json.RawMessage, key string) string {
		var value string
		_ = json.Unmarshal(fields[key], &value)
		return value
	}
	method := readString(envelope, "method")
	if method == "" {
		return http.StatusBadRequest, "A single JSON-RPC method is required"
	}
	var params map[string]json.RawMessage
	if raw := envelope["params"]; len(raw) != 0 && json.Unmarshal(raw, &params) != nil {
		return http.StatusBadRequest, "MCP params must be an object"
	}
	allowed := true
	switch method {
	case "tools/call":
		allowed = api.MCPEntryAllowed(p.ToolScopes, readString(params, "name"), scopes)
	case "prompts/get":
		allowed = api.MCPEntryAllowed(p.PromptScopes, readString(params, "name"), scopes)
	case "resources/read", "resources/subscribe", "resources/unsubscribe":
		allowed = api.MCPResourceAllowed(p.ResourceScopes, readString(params, "uri"), scopes)
	case "completion/complete":
		var ref map[string]json.RawMessage
		_ = json.Unmarshal(params["ref"], &ref)
		if readString(ref, "type") == "ref/prompt" {
			allowed = api.MCPEntryAllowed(p.PromptScopes, readString(ref, "name"), scopes)
		} else {
			allowed = api.MCPResourceAllowed(p.ResourceScopes, readString(ref, "uri"), scopes)
		}
	case "subscriptions/listen":
		var notifications map[string]json.RawMessage
		if json.Unmarshal(params["notifications"], &notifications) != nil {
			return http.StatusBadRequest, "Invalid subscription filters"
		}
		var resources []string
		if raw := notifications["resourceSubscriptions"]; len(raw) != 0 && json.Unmarshal(raw, &resources) != nil {
			return http.StatusBadRequest, "Invalid resource subscriptions"
		}
		for _, uri := range resources {
			allowed = allowed && api.MCPResourceAllowed(p.ResourceScopes, uri, scopes)
		}
	}
	if !allowed {
		return http.StatusForbidden, "Catalog entry is unavailable to this caller"
	}
	return 0, ""
}
