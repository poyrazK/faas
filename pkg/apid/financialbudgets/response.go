// Package financialbudgets projects retained monitoring state without implying
// that a notification policy enforces a spending ceiling.
package financialbudgets

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

func Response(ctx context.Context, store state.Store, p state.FinancialBudget, now time.Time) (api.FinancialBudgetResponse, error) {
	out := api.FinancialBudgetResponse{ID: p.ID, AccountID: p.AccountID, Revision: p.Revision, Spec: p.Spec, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt, DeletedAt: p.DeletedAt, Status: "draft", Reasons: []string{"enforcement_integration_pending"}}
	if p.DeletedAt != nil {
		out.Status = "deleted"
		return out, nil
	}
	if !p.Spec.Enabled {
		return out, nil
	}
	out.Status = "unavailable"
	monitor, ok := store.(state.FinancialBudgetMonitorStore)
	if !ok || !financial.NotificationOnly(p.Spec) {
		return out, nil
	}
	start := time.Date(now.UTC().Year(), now.UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	runtime, err := monitor.FinancialBudgetRuntime(ctx, p.AccountID, p.ID, p.Revision, start)
	if err != nil {
		return out, err
	}
	out.Status, out.NotificationReady, out.Runtime = "active", true, &runtime
	out.Reasons = []string{"notification_only_workloads_continue"}
	if runtime.Evaluation == nil {
		out.Reasons = append(out.Reasons, "awaiting_first_evaluation")
	} else if now.Before(runtime.Evaluation.EvaluatedAt) || now.Sub(runtime.Evaluation.EvaluatedAt) > api.FinancialEvidenceFreshness {
		runtime.Evaluation.Fresh = false
		runtime.Evaluation.Reasons = append(runtime.Evaluation.Reasons, "monitor_evaluation_stale")
	}
	return out, nil
}
