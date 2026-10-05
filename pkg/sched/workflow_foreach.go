package sched

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (o *WorkflowOrchestrator) executeForEach(ctx context.Context, run *state.WorkflowRun, parent *state.WorkflowStep, spec api.WorkflowStepSpec, workflowSpecs []api.WorkflowStepSpec) (bool, error) {
	outcome, err := o.store.ResolveWorkflowForEach(ctx, run.ID, parent.StepName)
	if errors.Is(err, state.ErrWorkflowForEachEvaluation) || errors.Is(err, state.ErrWorkflowForEachOutputLimit) {
		message := err.Error()
		if markErr := o.store.MarkWorkflowStepStatus(ctx, run.ID, parent.StepName, state.WorkflowStepStatusDead, 0, nil, &message); markErr != nil {
			return false, markErr
		}
		return true, nil
	}
	if err != nil || outcome.Complete || outcome.Item == nil {
		return outcome.Complete, err
	}
	action := spec.ForEach.Action.Step(outcome.Item.StepName)
	return o.executeStep(ctx, run, outcome.Item, action, nil, workflowSpecs)
}

// App item actions use the same current-attempt check as outbound calls. A
// cancellation or recovered lease stops the HTTP call; stale completions are
// additionally rejected by the store. No payloads enter this polling loop.
func (o *WorkflowOrchestrator) workflowItemContext(ctx context.Context, runID, name string, attempt int) (context.Context, func()) {
	callCtx, cancel := context.WithCancel(ctx)
	store, ok := o.store.(state.WorkflowOutboundStore)
	if !ok {
		cancel()
		return callCtx, cancel
	}
	first, err := store.GetWorkflowOutboundAttempt(callCtx, runID, name, attempt)
	if err != nil {
		cancel()
		return callCtx, cancel
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-callCtx.Done():
				return
			case <-ticker.C:
				current, err := store.GetWorkflowOutboundAttempt(callCtx, runID, name, attempt)
				if err != nil || current.Token != first.Token {
					cancel()
					return
				}
			}
		}
	}()
	return callCtx, func() { cancel(); <-done }
}
