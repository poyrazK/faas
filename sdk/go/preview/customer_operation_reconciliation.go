package faas

import (
	"context"
	"errors"
	"github.com/poyrazK/faas/sdk/go/internal/api"
)

type OperationWorkflowReconciliation = api.OperationWorkflowReconciliation
type OperationWorkflowReconciliationPayload = api.OperationWorkflowReconciliationPayload

// ReconcileWorkflowState compares an authoritative locked business row with a
// customer-scoped retained snapshot. It queues discrepancy evidence and, when
// safe, a fresh explicit state snapshot. It never reports a transition.
// SourceRevision is an application revision, not the SDK's allocated report revision.
func (tx *CustomerOperationTransaction) ReconcileWorkflowState(ctx context.Context, milestone string, scope string, subject OperationSubject, input OperationWorkflowReconciliation, read func(context.Context, OperationMilestoneListOptions) (OperationMilestonesResponse, error)) (result OperationWorkflowReconciliation, err error) {
	if tx == nil || !tx.open {
		return result, errors.New("faas: reconciliation requires an open callback")
	}
	defer func() {
		if err != nil && tx.guardErr == nil {
			tx.guardErr = err
		}
	}()
	if tx.guardErr != nil {
		return result, tx.guardErr
	}
	if ctx == nil || read == nil || scope == "" || subject.Type == "" || subject.ID == "" {
		return result, errors.New("faas: reconciliation requires scoped customer reader and business reference")
	}
	// Ignore caller-supplied observations; they are always derived from the reader.
	if _, err = api.EvaluateOperationWorkflowReconciliation(input, nil); err != nil {
		return result, err
	}
	page, err := read(ctx, OperationMilestoneListOptions{AppID: tx.input.appID, Scope: scope, SubjectType: subject.Type, SubjectID: subject.ID, Workflow: input.Workflow, WorkflowInstanceID: input.InstanceID, Limit: 1})
	if err != nil {
		return result, err
	}
	result, err = api.EvaluateOperationWorkflowReconciliation(input, page.WorkflowInstance)
	if err != nil || result.Status == "in_sync" {
		return result, err
	}
	staged := *tx
	staged.milestones = append([]OperationMilestoneRequest(nil), tx.milestones...)
	staged.workflowStates = append([]OperationWorkflowStateReport(nil), tx.workflowStates...)
	if result.RefreshNeeded() {
		if err = staged.WorkflowState(result.Workflow, result.InstanceID, result.AuthoritativeState); err != nil {
			return result, err
		}
	}
	if err = staged.Milestone(milestone, OperationWorkflowReconciliationPayload{Kind: api.OperationWorkflowReconciliationKind, Reconciliation: result}); err != nil {
		return result, err
	}
	if result.RefreshNeeded() {
		fact := staged.milestones[len(staged.milestones)-1]
		staged.workflowStates[len(staged.workflowStates)-1].EvidenceMilestones = []OperationWorkflowEvidenceMilestone{{ID: fact.ID, Name: fact.Name}}
	}
	tx.milestones, tx.workflowStates = staged.milestones, staged.workflowStates
	return result, nil
}
