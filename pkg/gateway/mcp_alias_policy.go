package gateway

import "net/http"

func (h *Handler) applyMCPAliasPolicy(w http.ResponseWriter, r *http.Request, app App) bool {
	if app.CanonicalHost == "" || app.CanonicalHost == hostname(r.Host) {
		return false
	}
	rule := h.edgeRules.MatchJWT(r.Context(), app.CanonicalHost, r.URL.Path, r.Method)
	if rule == nil {
		return false
	}
	if rule.Unavailable {
		h.rejectUnavailableEdgeRule(w, r, "jwt", rule.ID, "jwt_policy_unavailable")
		return true
	}
	if rule.MCP == nil {
		return false
	}
	if rule.AccountID != app.AccountID || rule.AppID != app.ID {
		h.rejectUnavailableEdgeRule(w, r, "jwt", rule.ID, "mcp_policy_owner_mismatch")
		return true
	}
	// Any alias-specific JWT gate still runs after this app-wide MCP gate.
	return h.applyMCPResourcePolicy(w, r, app, rule)
}
