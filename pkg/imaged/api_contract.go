package imaged

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/openapidiff"
	"github.com/onebox-faas/faas/pkg/state"
)

// checkAPIContract is the compute-side enforcement point. The snapshot is
// projected before MarkDeploymentLive, so a rejected production deployment
// can never become routable. Policies are app-scoped and production-only:
// observe is the migration-safe default, warn records telemetry, and block
// rejects breaking promotions. The legacy global flag remains the gate for
// the read-only diff API, not for an explicitly configured app policy.
func (h *Handler) checkAPIContract(ctx context.Context, dep state.Deployment) error {
	if !strings.EqualFold(strings.TrimSpace(dep.Scope), "prod") {
		return nil
	}
	app, err := h.store.AppByID(ctx, dep.AppID)
	if err != nil {
		return fmt.Errorf("api contract policy: load app: %w", err)
	}
	rawPolicy := strings.ToLower(strings.TrimSpace(app.OpenAPIContractPolicy))
	if rawPolicy != "" && !api.IsValidOpenAPIContractPolicy(rawPolicy) {
		return fmt.Errorf("api contract policy: invalid value %q", app.OpenAPIContractPolicy)
	}
	policy := api.NormalizeOpenAPIContractPolicy(rawPolicy)
	if policy == api.OpenAPIContractPolicyObserve {
		return nil
	}
	check, err := openapidiff.CheckPromotion(ctx, h.store, dep.AppID, dep.ID, "prod")
	if err != nil {
		if errors.Is(err, openapidiff.ErrSnapshotBaselineMissing) {
			return nil
		}
		if policy == api.OpenAPIContractPolicyWarn {
			if h.log != nil {
				h.log.Warn("api contract check unavailable", "app_id", dep.AppID, "deployment_id", dep.ID, "policy", policy, "err", err)
			}
			return nil
		}
		return fmt.Errorf("api contract check: %w", err)
	}
	if len(check.Diff.Breaks) == 0 {
		return nil
	}
	if policy == api.OpenAPIContractPolicyWarn {
		if h.log != nil {
			h.log.Warn("api contract breaking change allowed by warn policy", "app_id", dep.AppID, "deployment_id", dep.ID, "break_count", len(check.Diff.Breaks))
		}
		if h.audit != nil {
			h.audit.Emit(ctx, "deployment.api_contract_warning", &app.AccountID, map[string]any{
				"app_id": dep.AppID, "deployment_id": dep.ID, "scope": dep.Scope,
				"policy": policy, "break_count": len(check.Diff.Breaks),
			})
		}
		return nil
	}
	return &openapidiff.GateError{Diff: check.Diff}
}
