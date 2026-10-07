package state

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
)

// Only a service-authenticated native workflow envelope reaches this seam.
// Its live ledger association, not an HTTP operation header, owns private pins.
func resolveWorkflowOperationInvocationVersion(ctx context.Context, store invocationAppReader, app App, inv Invocation, headers map[string]string) (Invocation, InvocationVersion, bool, error) {
	adapter, ok := store.(WorkflowOperationStore)
	if !ok || headers["X-Faas-Workflow-Run-Id"] == "" {
		return inv, InvocationVersion{}, false, nil
	}
	runID := headers["X-Faas-Workflow-Run-Id"]
	op, exists, err := adapter.OperationForWorkflowRun(ctx, runID)
	if err != nil || !exists {
		return inv, InvocationVersion{}, exists, err
	}
	reader, ok := store.(interface {
		GetWorkflowRun(context.Context, string) (*WorkflowRun, error)
		GetWorkflowSteps(context.Context, string) ([]*WorkflowStep, error)
		GetWorkflowOutboundAttempt(context.Context, string, string, int) (WorkflowOutboundAttempt, error)
		DeploymentByID(context.Context, string) (Deployment, error)
	})
	if !ok {
		return inv, InvocationVersion{}, true, ErrConflict
	}
	run, err := reader.GetWorkflowRun(ctx, runID)
	if err != nil {
		return inv, InvocationVersion{}, true, err
	}
	if op.AppID != app.ID || op.AccountID != app.AccountID || app.Status == AppDeleted || !operationIsActive(op) || op.CancellationRequested || inv.PlatformTenantID != op.PlatformTenantID || run.PlatformTenantID != op.PlatformTenantID || run.AppID != op.AppID || run.ResumeCount+1 != op.Generation {
		return inv, InvocationVersion{}, true, ErrOperationStaleAttempt
	}
	name := headers["X-Faas-Workflow-Step"]
	attempt, err := strconv.Atoi(headers["X-Faas-Workflow-Attempt"])
	if err != nil || attempt < 1 {
		return inv, InvocationVersion{}, true, ErrInvalidArgument
	}
	if _, err := reader.GetWorkflowOutboundAttempt(ctx, runID, name, attempt); err != nil {
		return inv, InvocationVersion{}, true, ErrOperationStaleAttempt
	}
	spec := api.WorkflowRuntimeStep(run.DefinitionSnapshot, name)
	if spec == nil {
		return inv, InvocationVersion{}, true, ErrConflict
	}
	path, method := spec.Path, spec.Method
	if path == "" {
		path = "/" + spec.Run
	}
	if method == "" {
		method = "POST"
	}
	if inv.Path != path || inv.Method != method {
		return inv, InvocationVersion{}, true, ErrConflict
	}
	steps, err := reader.GetWorkflowSteps(ctx, runID)
	if err != nil {
		return inv, InvocationVersion{}, true, err
	}
	matched := false
	for _, step := range steps {
		if step.StepName == name && step.Attempt == attempt && equalWorkflowJSON(step.Input, inv.Payload) {
			matched = true
		}
	}
	if !matched {
		return inv, InvocationVersion{}, true, ErrConflict
	}
	revision, release, err := invocationPinHeaders(headers)
	if err != nil || (op.ReleaseID != "" && (release != op.ReleaseID || revision != "")) || (op.ReleaseID == "" && (revision != op.DeploymentID || release != "")) {
		return inv, InvocationVersion{}, true, ErrConflict
	}
	deployment, err := reader.DeploymentByID(ctx, op.DeploymentID)
	if err != nil || deployment.AppID != app.ID || deployment.Scope != op.Scope || deployment.Status != DeployLive {
		return inv, InvocationVersion{}, true, ErrConflict
	}
	inv.AccountID, inv.DeploymentScope = op.AccountID, op.Scope
	inv.Headers, err = json.Marshal(headers)
	return inv, InvocationVersion{DeploymentID: op.DeploymentID, ReleaseID: op.ReleaseID, Scope: op.Scope}, true, err
}
