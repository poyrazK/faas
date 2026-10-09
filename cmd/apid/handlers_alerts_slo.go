package main

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// validateSLOAlert mirrors alert_rules_slo_chk at the API boundary: the
// ADR-747 SLO metrics need an slo_id naming one of this app's SLOs, and
// every other metric must leave it empty. Checking ownership here keeps a
// rule from referencing another app's SLO by id.
func (s *server) validateSLOAlert(ctx context.Context, appID string, req api.CreateAlertRuleRequest) *api.Problem {
	if !api.IsSLOAlertMetric(req.Metric) {
		if req.SLOID != "" {
			return api.ErrAlertRuleInvalid("slo_id applies only to slo_budget_burn and slo_budget_remaining_pct")
		}
		return nil
	}
	if req.SLOID == "" {
		return api.ErrAlertRuleInvalid("slo_id is required for " + req.Metric + "; list SLOs with GET /v1/apps/{slug}/slos")
	}
	store, ok := s.store.(state.SLOStore)
	if !ok {
		return api.ErrCapacity("SLO definitions are unavailable on this deployment")
	}
	if _, err := store.GetSLO(ctx, appID, req.SLOID); err != nil {
		return api.ErrAlertRuleInvalid("slo_id does not name an SLO on this app")
	}
	return nil
}
