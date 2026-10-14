package edgerules

// ADR-1192: alert rules on edge-rule activity. The alert handlers stay in
// apidserver; the edge-rule specific shape check and rule lookup live here.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ValidateActivityAlert checks the edge-rule metric family's shape: the
// windows hourly hit buckets can answer, and edge_rule_id only on those
// metrics. Mirrors the alert_rules_edge_rule_chk constraint.
func ValidateActivityAlert(metric, edgeRuleID, windowSpec string) *api.Problem {
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

// ResolveActivityAlertRule returns the canonical id of an app's edge rule
// for an activity alert, so the evaluator compares ids in one form. A rule
// of another app is reported as not found.
func (s *Service) ResolveActivityAlertRule(ctx context.Context, appID, ruleID string) (string, *api.Problem) {
	rule, err := s.Host.Store().GetEdgeRuleByID(ctx, ruleID)
	if errors.Is(err, state.ErrNotFound) || (err == nil && rule.AppID != appID) {
		return "", api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such edge rule on this app")
	}
	if err != nil {
		return "", api.ErrInternal("edge-rule activity alerts")
	}
	return rule.ID, nil
}
