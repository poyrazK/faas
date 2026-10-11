package main

// ADR-974: alert rules on edge-rule activity.

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// validateEdgeRuleAlert checks the edge-rule metric family's shape: the
// windows hourly hit buckets can answer, and edge_rule_id only on those
// metrics. Mirrors the alert_rules_edge_rule_chk constraint.
func validateEdgeRuleAlert(metric, edgeRuleID, windowSpec string) *api.Problem {
	if !api.IsEdgeRuleAlertMetric(metric) {
		if edgeRuleID != "" {
			return api.ErrAlertRuleInvalid("edge_rule_id applies only to edge_rule_hits and edge_rule_log_hits")
		}
		return nil
	}
	ok := false
	for _, w := range api.EdgeRuleAlertWindowSpecs {
		ok = ok || w == windowSpec
	}
	if !ok {
		return api.ErrAlertRuleInvalid(fmt.Sprintf("edge-rule activity alerts use window_spec %s (hit counts are hourly, kept 14 days)", strings.Join(api.EdgeRuleAlertWindowSpecs, ", ")))
	}
	if edgeRuleID != "" {
		if _, err := uuid.Parse(edgeRuleID); err != nil {
			return api.ErrAlertRuleInvalid("edge_rule_id is not a valid id")
		}
	}
	return nil
}

// checkEdgeRuleAlertTarget resolves req.EdgeRuleID to a rule of this app
// and canonicalises it, so the evaluator compares ids in one form.
func (s *server) checkEdgeRuleAlertTarget(w http.ResponseWriter, r *http.Request, appID string, req *api.CreateAlertRuleRequest) bool {
	if req.EdgeRuleID == "" || !api.IsEdgeRuleAlertMetric(req.Metric) {
		return true
	}
	rule, err := s.store.GetEdgeRuleByID(r.Context(), req.EdgeRuleID)
	if errors.Is(err, state.ErrNotFound) || (err == nil && rule.AppID != appID) {
		s.notFound(w, "no such edge rule on this app")
		return false
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("edge-rule activity alerts"))
		return false
	}
	req.EdgeRuleID = rule.ID
	return true
}
