package sched

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workflowcode"
)

// ADR-20261113120824 §2: wf.call(name, input) resolves name against the
// workflow app's function_callables and enqueues the callee's async
// invocation with the operation, in one transaction. The operation parks
// until the invocation is terminal; its transaction settles it.

// workflowCallPoll bounds how long a waiting call goes unchecked. Settlement
// wakes the run at once; this only covers a settlement that lands while the
// engine is mid-pass on the run.
const workflowCallPoll = time.Minute

// codeWorkflowCall builds a call operation's callee invocation. A name the
// app may not call fails the operation (message, nil error); a store failure
// is returned as an error so the pass is retried.
func (o *WorkflowOrchestrator) codeWorkflowCall(ctx context.Context, run *state.WorkflowRun, op workflowcode.PlannedOp) (*state.Invocation, string, error) {
	caller, err := o.store.AppByID(ctx, run.AppID)
	if err != nil {
		return nil, "", fmt.Errorf("workflow call: workflow app: %w", err)
	}
	slug, ok := caller.Manifest.FunctionCallables[op.Function]
	if !ok {
		return nil, fmt.Sprintf("function %q is not callable from this workflow", op.Function), nil
	}
	callee, err := o.store.AppBySlug(ctx, slug)
	if errors.Is(err, state.ErrNotFound) || (err == nil && callee.AccountID != caller.AccountID) {
		return nil, fmt.Sprintf("function %q (app %s) does not exist", op.Function, slug), nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("workflow call: callee: %w", err)
	}
	// The call lands in the run's environment, as a function's send() does.
	scope := ""
	if run.DeploymentID != "" {
		deployment, err := o.store.DeploymentByID(ctx, run.DeploymentID)
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			return nil, "", fmt.Errorf("workflow call: run deployment: %w", err)
		}
		scope = deployment.Scope
	}
	headers, err := json.Marshal(map[string]string{
		"Content-Type":           "application/json",
		api.FunctionCallHeader:   "1",
		"Idempotency-Key":        workflowStepIdempotencyKey(run.ID, op.Name),
		"X-Gregale-Workflow-Run": run.ID,
	})
	if err != nil {
		return nil, "", err
	}
	payload := op.Input
	if len(payload) == 0 {
		payload = json.RawMessage(`null`)
	}
	return &state.Invocation{
		ID: uuid.NewString(), AppID: callee.ID, AccountID: callee.AccountID,
		Source: state.InvocationAsyncInvoke, State: state.InvocationPending, Method: "POST", Path: "/",
		Payload: payload, Headers: headers, DueAt: time.Now().UTC(), DeploymentScope: scope,
		SentByAppID: run.AppID, WorkflowCallRunID: run.ID, WorkflowCallOp: op.Name,
	}, "", nil
}

// codeCallWrite is the row write for a new call operation: pending with its
// invocation, or failed when the callee cannot be called.
func (o *WorkflowOrchestrator) codeCallWrite(ctx context.Context, run *state.WorkflowRun, op workflowcode.PlannedOp) (state.WorkflowCodeOpWrite, error) {
	call, message, err := o.codeWorkflowCall(ctx, run, op)
	if err != nil {
		return state.WorkflowCodeOpWrite{}, err
	}
	descriptor := op.Descriptor
	if call == nil {
		return state.WorkflowCodeOpWrite{StepName: op.Name, New: true, Input: workflowcode.EncodeDescriptor(descriptor),
			Status: state.WorkflowStepStatusFailed, Attempt: 1, Error: &message}, nil
	}
	descriptor.InvocationID = call.ID
	return state.WorkflowCodeOpWrite{StepName: op.Name, New: true, Input: workflowcode.EncodeDescriptor(descriptor),
		Status: state.WorkflowStepStatusPending, Call: call}, nil
}
