// adr:1360 — one frozen admission budget for every driver of a receipt.
package state

import (
	"github.com/onebox-faas/faas/pkg/api"
)

// VerificationRunBudgetError is temporary backpressure, not review drift.
type VerificationRunBudgetError struct{ Limit, Observed int }

func (e *VerificationRunBudgetError) Error() string { return "binding verification budget full" }

// Collect exact resource identities, deduplicating borrowed references. Expiry
// or a cancellation request is not proof that underlying work has stopped.
func verificationBudgetSelection(root string, limit int, runs []verificationRunRecord) (api.BindingVerificationBudget, map[string]string, error) {
	out := api.BindingVerificationBudget{RunID: root, MaxConcurrency: api.BindingVerificationConcurrency(limit)}
	if out.MaxConcurrency < 1 || out.MaxConcurrency > api.BindingVerificationRunnerMaxConcurrency {
		return out, nil, ErrConflict
	}
	refs := map[string]string{}
	for _, r := range runs {
		if api.BindingVerificationConcurrency(r.Report.MaxConcurrency) != out.MaxConcurrency {
			return out, nil, ErrConflict
		}
		for _, step := range r.Report.Steps {
			if step.ResourceID == "" {
				if r.Report.Status == "pending" && (step.Plan.Action == "probe" || step.Plan.Action == "queue-canary") {
					out.WaitingSteps++
				}
				continue
			}
			if step.ResourceKind != "task" && step.ResourceKind != "queue" {
				return out, nil, ErrConflict
			}
			key := step.ResourceKind + "/" + step.ResourceID
			if owner, ok := refs[key]; ok && owner != r.Report.Policy.AppID {
				return out, nil, ErrConflict
			}
			refs[key] = r.Report.Policy.AppID
		}
	}
	return out, refs, nil
}
