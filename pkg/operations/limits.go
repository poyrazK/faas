package operations

import (
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

// compileLimits validates ADR-1196 definition limits against the plan. An
// absent block leaves the revision unchanged; an empty one is rejected so two
// spellings of "no limits" cannot produce different revisions.
func compileLimits(spec *api.OperationDefinitionSpec, plan api.OperationPlanLimits) error {
	if spec.Limits == nil {
		return nil
	}
	l := *spec.Limits
	switch {
	case spec.Job != "":
		return fmt.Errorf("job operations use their run deadline; limits are not supported")
	case l == api.OperationLimits{}:
		return fmt.Errorf("operation limits must declare at least one bound")
	case l.DeadlineSeconds < 0 || l.DeadlineSeconds > plan.DeadlineSecondsMax:
		return fmt.Errorf("operation deadline_seconds must be between 1 and %d", plan.DeadlineSecondsMax)
	case l.MaxAttempts < 0 || l.MaxAttempts > plan.AttemptsMax:
		return fmt.Errorf("operation max_attempts must be between 1 and %d", plan.AttemptsMax)
	case l.MaxAttempts > 0 && spec.Workflow != "":
		// ADR-20261016110803: workflow steps never repeat automatically; a failed step
		// needs reconciliation. Their budget is max_execution_seconds.
		return fmt.Errorf("workflow steps do not retry automatically; max_attempts applies to HTTP operations")
	case l.MaxExecutionSeconds < 0 || l.MaxExecutionSeconds > plan.ExecutionSecondsMax:
		return fmt.Errorf("operation max_execution_seconds must be between 1 and %d", plan.ExecutionSecondsMax)
	}
	spec.Limits = &l
	return nil
}
