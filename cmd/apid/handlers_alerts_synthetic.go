package main

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// validateSyntheticCheckAlert mirrors alert_rules_synthetic_check_chk: the
// ADR-748 check metrics need a synthetic_check_id naming one of this app's
// checks, and every other metric must leave it empty.
func (s *server) validateSyntheticCheckAlert(ctx context.Context, appID string, req api.CreateAlertRuleRequest) *api.Problem {
	if !api.IsSyntheticCheckAlertMetric(req.Metric) {
		if req.SyntheticCheckID != "" {
			return api.ErrAlertRuleInvalid("synthetic_check_id applies only to synthetic_check_consecutive_failures and synthetic_check_latency_p95_ms")
		}
		return nil
	}
	if req.SyntheticCheckID == "" {
		return api.ErrAlertRuleInvalid("synthetic_check_id is required for " + req.Metric + "; list checks with GET /v1/apps/{slug}/synthetics")
	}
	store, ok := s.store.(state.SyntheticCheckStore)
	if !ok {
		return api.ErrCapacity("synthetic checks are unavailable on this deployment")
	}
	if _, err := store.GetSyntheticCheck(ctx, appID, req.SyntheticCheckID); err != nil {
		return api.ErrAlertRuleInvalid("synthetic_check_id does not name a synthetic check on this app")
	}
	return nil
}
