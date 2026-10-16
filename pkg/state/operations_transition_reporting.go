package state

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func workflowTransitionReporting(current api.OperationWorkflowState, observations []workflowBottleneckObservation, steps []api.OperationWorkflowSpec, available bool, at time.Time) *api.OperationWorkflowTransitionReporting {
	at = at.UTC().Truncate(time.Microsecond)
	out := &api.OperationWorkflowTransitionReporting{EvaluatedAt: at, State: current.State, ReportingOperationID: current.OperationID, Status: "definition_unavailable", Conditions: []api.OperationWorkflowConditionCoverageEntry{}}
	if !available {
		return out
	}
	if current.Terminal {
		out.Status = "terminal"
		return out
	}
	instance := workflowPerformanceInstance{Current: current, CurrentConditionSteps: workflowConditionSteps(steps, current.Workflow, current.ContractVersion), Observations: observations}
	instance.Bottlenecks = calculateWorkflowBottlenecks(observations, &current, nil, at, false)
	item, status := workflowConditionCoverageInstance(instance, at)
	if status != "" {
		out.Status = status
		return out
	}
	out.Status = "compared"
	out.ExpectedConditionCount = item.ExpectedConditionCount
	out.ReportedConditionCount = item.ReportedConditionCount
	out.MissingConditionCount = item.MissingConditionCount
	out.OverdueConditionCount = item.OverdueConditionCount
	out.Conditions = item.Conditions
	if len(out.Conditions) > api.OperationWorkflowConditionRefreshEvidenceMax {
		out.Conditions = out.Conditions[:api.OperationWorkflowConditionRefreshEvidenceMax]
		out.ConditionsTruncated = true
	}
	return out
}
func attachTransitionReporting(out *api.OperationWorkflowTransitionReadiness, instance *api.OperationWorkflowInstanceSnapshot, operation string) {
	out.Reporting = instance.ConditionReporting
	if out.Reporting != nil {
		switch out.Reporting.Status {
		case "compared":
			if out.Reporting.MissingConditionCount > 0 {
				out.Advisories = append(out.Advisories, "condition_reports_missing")
			}
			if out.Reporting.OverdueConditionCount > 0 {
				out.Advisories = append(out.Advisories, "condition_reports_overdue")
			}
		case "unconfigured", "terminal":
		default:
			out.Advisories = append(out.Advisories, "condition_reporting_unknown")
		}
	}
	pending := selectedVerifications(instance.ReadinessVerifications, api.OperationWorkflowAttentionOptions{TargetOperation: operation})
	out.PendingVerificationCount = int64(len(pending))
	if len(pending) > 0 {
		out.Advisories = append(out.Advisories, "verification_pending")
		out.PendingVerifications, _ = verificationPreview(pending, api.OperationWorkflowAttentionOptions{})
		out.PendingVerificationsTruncated = len(pending) > len(out.PendingVerifications)
	}
}
