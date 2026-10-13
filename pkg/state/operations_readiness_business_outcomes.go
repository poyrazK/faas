package state

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
	"time"
)

// Query the source identity independently of readiness edges: terminal outcomes have no next edge.
func readReadinessBusinessOutcome(ctx context.Context, store readinessSubscriptionBackend, account, tenant, app, scope string, n api.OperationWorkflowReadinessNotification, expected string, operator bool) (*api.OperationWorkflowReadinessBusinessOutcome, error) {
	out := &api.OperationWorkflowReadinessBusinessOutcome{ExpectedOutcomeCode: expected, CheckedAt: time.Now().UTC().Truncate(time.Microsecond), Status: "unknown", Explanation: "No retained application state is available for this source workflow."}
	saved := readinessNotificationBaseline(n)
	if saved == nil {
		return out, nil
	}
	opts := api.OperationMilestoneListOptions{ReadinessOnly: true, AppID: app, Scope: scope, SubjectType: saved.Subject.Type, SubjectID: saved.Subject.ID, Workflow: saved.Workflow, WorkflowInstanceID: saved.InstanceID, Limit: 1}
	var projection api.OperationMilestonesResponse
	var err error
	if operator {
		opts.TenantID = saved.PlatformTenantID
		projection, err = store.ListAccountOperationMilestones(ctx, account, opts)
	} else {
		projection, err = store.ListPlatformTenantOperationMilestones(ctx, account, tenant, opts)
	}
	if err != nil {
		return nil, err
	}
	out.CheckedAt = time.Now().UTC().Truncate(time.Microsecond)
	if projection.WorkflowInstance == nil || projection.WorkflowInstance.State == nil {
		return out, nil
	}
	state := projection.WorkflowInstance.State
	out.ReportingOperationID, out.ReportID = state.OperationID, state.ReportID
	out.StateRevision, out.ContractVersion, out.State = state.Revision, effectiveWorkflowContractVersion(state.ContractVersion), state.State
	out.ReportedOutcomeCode, out.ReportedOutcomeDescription = state.OutcomeCode, state.OutcomeDescription
	if !state.OccurredAt.IsZero() {
		occurred := state.OccurredAt
		out.OccurredAt = &occurred
	}
	if out.ContractVersion != effectiveWorkflowContractVersion(saved.ContractVersion) || state.Revision < saved.StateRevision {
		out.Explanation = "Contract changed or retained state precedes the source observation; outcome cannot be compared."
		return out, nil
	}
	if state.Stale || operationWorkflowStateIsStale(out.CheckedAt, *state) {
		out.Explanation = "The application state report is stale; no business result is inferred."
		return out, nil
	}
	if !state.Terminal {
		out.Status, out.Explanation = "pending", "The application reports a nonterminal workflow; the expected terminal outcome has not been reported."
	} else if state.OutcomeCode == "" {
		out.Explanation = "The application reports terminal state without an explicit outcome code; success is unknown."
	} else if state.OutcomeCode == expected {
		out.Status, out.Explanation = "achieved", "The application explicitly reports the expected terminal outcome. This does not establish that the follow-up caused it."
	} else {
		out.Status, out.Explanation = "different_outcome", "The application explicitly reports a different terminal outcome; its business meaning remains application-defined."
	}
	return out, nil
}
