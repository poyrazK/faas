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
	if origins := r.Header.Values("Origin"); len(origins) > 1 || (len(origins) == 1 && !slices.Contains(p.AllowedOrigins, origins[0])) {
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
		w.Header().Add("Vary", "Access-Control-Request-Method, Access-Control-Request-Headers")
		methods := r.Header.Values("Access-Control-Request-Method")
		if len(methods) != 1 || !slices.Contains([]string{"GET", "POST", "DELETE", "OPTIONS"}, methods[0]) {
			http.Error(w, "MCP preflight method is not allowed", http.StatusBadRequest)
			return true
		}
		headers, ok := mcpPreflightHeaders(r.Header.Values("Access-Control-Request-Headers"))
		if !ok {
			http.Error(w, "MCP preflight header is not allowed", http.StatusBadRequest)
			return true
		}
		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", strings.Join(headers, ", "))
	}
	w.WriteHeader(http.StatusNoContent)
	return true
}

// Reflect only explicitly supported headers and well-formed annotated parameter
// names. Never widen the caller's configured Origin policy.
func mcpPreflightHeaders(values []string) ([]string, bool) {
	headers := []string{}
	for _, value := range values {
		for _, raw := range strings.Split(value, ",") {
			name := strings.ToLower(strings.TrimSpace(raw))
			if name == "" {
				continue
			}
			switch name {
			case "authorization", "content-type", "accept", "mcp-protocol-version", "mcp-session-id", "mcp-method", "mcp-name":
			default:
				suffix, ok := strings.CutPrefix(name, "mcp-param-")
				if !ok || suffix == "" {
					return nil, false
				}
				for _, ch := range suffix {
					valid := ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || strings.ContainsRune("!#$%&'*+.^_`|~-", ch)
					if !valid {
						return nil, false
					}
				}
			}
			if !slices.Contains(headers, name) {
				headers = append(headers, name)
			}
		}
	}
	return headers, true
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
	verifyRule.ExtractClaims = append(verifyRule.ExtractClaims, h.throttleJWTClaimNames(r, app)...)
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
	if json.Unmarshal(body, &envelope) != nil || !mcpPolicyFieldsUnambiguous(body, "method", "params") {
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
	var fields []string
	switch method {
	case "tools/call", "prompts/get":
		fields = []string{"name"}
	case "resources/read", "resources/subscribe", "resources/unsubscribe":
		fields = []string{"uri"}
	case "completion/complete":
		fields = []string{"ref"}
	case "subscriptions/listen":
		fields = []string{"notifications"}
	}
	if len(fields) > 0 && len(envelope["params"]) > 0 && !mcpPolicyFieldsUnambiguous(envelope["params"], fields...) {
		return http.StatusBadRequest, "Ambiguous MCP request parameters"
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
		if len(params["ref"]) > 0 && !mcpPolicyFieldsUnambiguous(params["ref"], "type", "name", "uri") {
			return http.StatusBadRequest, "Ambiguous MCP completion reference"
		}
		_ = json.Unmarshal(params["ref"], &ref)
		if readString(ref, "type") == "ref/prompt" {
			allowed = api.MCPEntryAllowed(p.PromptScopes, readString(ref, "name"), scopes)
		} else {
			allowed = api.MCPResourceAllowed(p.ResourceScopes, readString(ref, "uri"), scopes)
		}
	case "subscriptions/listen":
		var notifications map[string]json.RawMessage
		if json.Unmarshal(params["notifications"], &notifications) != nil || !mcpPolicyFieldsUnambiguous(params["notifications"], "resourceSubscriptions") {
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

// Security-relevant protocol fields must have one canonical spelling. Go's
// struct decoder matches names case-insensitively while map lookups do not;
// forwarding ambiguous bytes could authorize a different request than the guest
// executes. Arguments and unrelated extension fields remain untouched.
func mcpPolicyFieldsUnambiguous(body []byte, protected ...string) bool {
	decoder := json.NewDecoder(bytes.NewReader(body))
	token, err := decoder.Token()
	if err != nil {
		return false
	}
	if token == nil { // A null optional params object has no fields.
		return true
	}
	if token != json.Delim('{') {
		return false
	}
	seen := make(map[string]bool, len(protected))
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			return false
		}
		key, ok := token.(string)
		if !ok {
			return false
		}
		for _, name := range protected {
			if strings.EqualFold(key, name) {
				if key != name || seen[name] {
					return false
				}
				seen[name] = true
			}
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return false
		}
	}
	_, err = decoder.Token()
	return err == nil
}
