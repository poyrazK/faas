package gitops

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
)

// RedactPlan keeps the reviewed identity and actions without returning customer
// values. The original plan remains available for server-side hash validation.
func RedactPlan(plan api.EnvironmentGitOpsPlan) api.EnvironmentGitOpsPlan {
	plan.Changes = slices.Clone(plan.Changes)
	for i := range plan.Changes {
		plan.Changes[i].Before, plan.Changes[i].After = nil, nil
	}
	return plan
}

// RedactRuns projects stored reports onto the public plan contract. Malformed
// historical JSON fails closed instead of falling back to the unredacted bytes.
func RedactRuns(runs []api.EnvironmentGitOpsRun) ([]api.EnvironmentGitOpsRun, error) {
	out := make([]api.EnvironmentGitOpsRun, len(runs))
	copy(out, runs)
	for i := range out {
		var plan api.EnvironmentGitOpsPlan
		if err := json.Unmarshal(out[i].Plan, &plan); err != nil {
			return nil, fmt.Errorf("redact GitOps report plan: %w", err)
		}
		raw, err := json.Marshal(RedactPlan(plan))
		if err != nil {
			return nil, fmt.Errorf("encode redacted GitOps report plan: %w", err)
		}
		out[i].Plan = raw
	}
	return out, nil
}
