package mcpmanagement

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
)

// Only fixed, scoped metadata comparisons enter this projection. Opaque
// configuration/preview baselines reveal a change marker, never their values.
type planDrift struct {
	Field         string `json:"field"`
	Expected      any    `json:"expected,omitempty"`
	Observed      any    `json:"observed,omitempty"`
	ValuesOmitted bool   `json:"values_omitted,omitempty"`
}

func addPlanDrift[T string | int64 | bool](out *planCheck, field string, expected, observed T) {
	if expected != observed {
		out.Drift = append(out.Drift, planDrift{Field: field, Expected: expected, Observed: observed})
	}
}

func (out *planCheck) hiddenDrift(field string, changed bool) {
	if changed {
		out.Drift = append(out.Drift, planDrift{Field: field, ValuesOmitted: true})
	}
}

func (m *management) checkReleaseBaseline(ctx context.Context, p *releasePlan, out *planCheck) error {
	fresh, err := m.captureScopedPlan(ctx, p.App, p.Project, planEnvironment(p), p.Reason, p.Action)
	if err != nil {
		return err
	}
	if fresh.AccountID != p.AccountID || fresh.AppID != p.AppID || fresh.EnvironmentID != p.EnvironmentID {
		out.hiddenDrift("release_identity", true)
		return invalid("plan_stale", "release identity changed")
	}
	for _, id := range []string{p.ExpectedCurrentID, fresh.ExpectedCurrentID, p.latestID, fresh.latestID} {
		if id != "" && validateSegment(id, "deployment_id") != nil {
			return invalid("invalid_response", "release identifiers are invalid")
		}
	}
	out.hiddenDrift("app_settings", fresh.appHash != p.appHash)
	out.hiddenDrift("configuration_baseline", fresh.review.ConfigurationHash != p.review.ConfigurationHash)
	addPlanDrift(out, "serving_deployment_id", p.ExpectedCurrentID, fresh.ExpectedCurrentID)
	addPlanDrift(out, "latest_deployment_id", p.latestID, fresh.latestID)
	if len(out.Drift) > 0 {
		return invalid("plan_stale", "app configuration or release changed since planning")
	}
	return nil
}

func (m *management) checkEnvironmentRollbackBaseline(ctx context.Context, p *environmentPlan, out *planCheck) error {
	value, err := m.getEnvironmentPromotion(ctx, environmentPromotionArgs{Project: p.Project, Environment: p.Environment, PromotionID: p.PromotionID})
	if err != nil {
		return err
	}
	status := value.(api.ProjectEnvironmentPromotionStatusResponse)
	if status.FromEnvironment != p.FromEnvironment {
		out.hiddenDrift("promotion_identity", true)
		return invalid("plan_stale", "promotion source changed")
	}
	out.hiddenDrift("promotion_baseline", status.PromotionHash != p.PromotionHash)
	addPlanDrift(out, "promotion_running", false, status.Status == "running")
	addPlanDrift(out, "rollback_started", false, status.RollbackStatus != "")
	if len(out.Drift) > 0 {
		return invalid("plan_stale", "promotion is no longer a rollback candidate")
	}
	out.BaselineCheck = "partial_native_rollback_guard_required"
	return nil
}
