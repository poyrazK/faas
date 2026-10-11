package state

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
	"time"
)

type OperationWorkflowActionPreviewStore interface {
	PreviewAccountWorkflowActions(context.Context, string, string, api.OperationWorkflowActionPreviewRequest) (api.OperationWorkflowActionPreviewResponse, error)
	PreviewPlatformTenantWorkflowActions(context.Context, string, string, api.OperationWorkflowActionPreviewRequest) (api.OperationWorkflowActionPreviewResponse, error)
}

func previewOperationWorkflowActions(ctx context.Context, store OperationMilestoneStore, account, tenant, app string, req api.OperationWorkflowActionPreviewRequest, operator bool) (api.OperationWorkflowActionPreviewResponse, error) {
	validation := api.OperationWorkflowReadinessRequest{AppID: req.AppID, TenantID: req.TenantID, Scope: req.Scope, Subject: req.Subject, Workflow: req.Workflow, InstanceID: req.InstanceID, Operation: "preview", FromState: "preview", ToState: "preview", StateRevision: req.StateRevision, ContractVersion: req.ContractVersion}
	if req.Operation != "" {
		validation.Operation = req.Operation
	}
	if err := validateOperationWorkflowReadiness(validation, operator); err != nil {
		return api.OperationWorkflowActionPreviewResponse{}, err
	}
	opts := api.OperationMilestoneListOptions{ReadinessOnly: true, AppID: app, Scope: req.Scope, TenantID: req.TenantID, SubjectType: req.Subject.Type, SubjectID: req.Subject.ID, Workflow: req.Workflow, WorkflowInstanceID: req.InstanceID, Limit: 1}
	var page api.OperationMilestonesResponse
	var err error
	if operator {
		page, err = store.ListAccountOperationMilestones(ctx, account, opts)
	} else {
		opts.AppID = req.AppID
		page, err = store.ListPlatformTenantOperationMilestones(ctx, account, tenant, opts)
	}
	if err != nil {
		return api.OperationWorkflowActionPreviewResponse{}, err
	}
	at := time.Now().UTC().Truncate(time.Microsecond)
	out := api.OperationWorkflowActionPreviewResponse{Subject: req.Subject, Workflow: req.Workflow, InstanceID: req.InstanceID, EvaluatedAt: at, Actions: make([]api.OperationWorkflowTransitionReadiness, 0), Reason: "state_unknown"}
	instance := page.WorkflowInstance
	if instance == nil {
		return out, nil
	}
	out.ContractVersion = instance.ContractVersion
	if instance.State == nil {
		return out, nil
	}
	out.State = instance.State
	out.StateRevision = instance.State.Revision
	instance.State.Stale = operationWorkflowStateIsStale(at, *instance.State)
	evaluateOperationWorkflowDeadline(at, instance.State)
	if instance.State.Terminal {
		out.Reason = "terminal"
		return out, nil
	}
	out.Reason = "no_declared_transition"
	for _, edge := range instance.AllowedTransitions {
		if edge.From != instance.State.State || req.Operation != "" && edge.Operation != req.Operation {
			continue
		}
		out.ActionCount++
		if len(out.Actions) >= 100 {
			continue
		}
		candidate := validation
		candidate.Operation = edge.Operation
		candidate.FromState = edge.From
		candidate.ToState = edge.To
		out.Actions = append(out.Actions, evaluateOperationWorkflowReadiness(instance, candidate))
	}
	if out.ActionCount > 0 {
		out.Reason = "actions_available"
	}
	out.HasMore = out.ActionCount > len(out.Actions)
	return out, nil
}
func (m *MemStore) PreviewAccountWorkflowActions(ctx context.Context, account, app string, req api.OperationWorkflowActionPreviewRequest) (api.OperationWorkflowActionPreviewResponse, error) {
	return previewOperationWorkflowActions(ctx, m, account, req.TenantID, app, req, true)
}
func (m *MemStore) PreviewPlatformTenantWorkflowActions(ctx context.Context, account, tenant string, req api.OperationWorkflowActionPreviewRequest) (api.OperationWorkflowActionPreviewResponse, error) {
	return previewOperationWorkflowActions(ctx, m, account, tenant, req.AppID, req, false)
}
func (s *PgStore) PreviewAccountWorkflowActions(ctx context.Context, account, app string, req api.OperationWorkflowActionPreviewRequest) (api.OperationWorkflowActionPreviewResponse, error) {
	return previewOperationWorkflowActions(ctx, s, account, req.TenantID, app, req, true)
}
func (s *PgStore) PreviewPlatformTenantWorkflowActions(ctx context.Context, account, tenant string, req api.OperationWorkflowActionPreviewRequest) (api.OperationWorkflowActionPreviewResponse, error) {
	return previewOperationWorkflowActions(ctx, s, account, tenant, req.AppID, req, false)
}
