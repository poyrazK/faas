package gateway

import (
	"net/http"
	"strconv"
)

// applyEdgeRuleRespond serves a bounded fixed JSON response for a preview
// route. It is intentionally placed after the app authentication gates by the
// caller, but before cache lookup and backend wake/admission.
func (h *Handler) applyEdgeRuleRespond(w http.ResponseWriter, r *http.Request, app App) bool {
	if h.edgeRules == nil {
		return false
	}
	rule := h.edgeRules.MatchRespond(r.Context(), hostname(r.Host), r.URL.Path, r.Method)
	if rule == nil {
		if h.metrics != nil {
			h.metrics.ObserveEdgeRuleMatch("respond", "miss")
		}
		return false
	}
	if !app.IsPreview || rule.AccountID != app.AccountID || rule.AppID != app.ID {
		if h.edgeRuleAudit != nil {
			h.edgeRuleAudit.Emit(r.Context(), "edge_rule.respond_blocked", &rule.AccountID, map[string]any{
				"rule_id":        rule.ID,
				"rule_app_id":    rule.AppID,
				"request_app_id": app.ID,
				"request_host":   r.Host,
			})
		}
		if h.metrics != nil {
			h.metrics.ObserveEdgeRuleMatch("respond", "blocked")
			h.metrics.ObserveEdgeRuleApply("respond", "success")
		}
		return false
	}

	w.Header().Set("Content-Type", "application/json")
	if len(rule.Body) > 0 {
		w.Header().Set("Content-Length", strconv.Itoa(len(rule.Body)))
	}
	w.WriteHeader(rule.StatusCode)
	if r.Method != http.MethodHead && len(rule.Body) > 0 && rule.StatusCode != http.StatusNoContent && rule.StatusCode != http.StatusNotModified {
		_, _ = w.Write(rule.Body)
	}
	if h.edgeRuleAudit != nil {
		h.edgeRuleAudit.Emit(r.Context(), "edge_rule.respond_matched", nil, map[string]any{
			"rule_id":     rule.ID,
			"method":      r.Method,
			"path":        r.URL.Path,
			"status_code": rule.StatusCode,
		})
	}
	if h.metrics != nil {
		h.metrics.ObserveEdgeRuleMatch("respond", "match")
		h.metrics.ObserveEdgeRuleApply("respond", "success")
	}
	return true
}
