package sched

import (
	"context"
	"errors"
	"sync"
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
	if err != nil || outcome.Complete {
		return outcome.Complete, err
	}
	items := outcome.Items
	if len(items) == 0 && outcome.Item != nil {
		items = []*state.WorkflowStep{outcome.Item}
	}
	if len(items) == 0 {
		return false, nil
	}
	stepRows, err := o.store.GetWorkflowSteps(ctx, run.ID)
	if err != nil {
		return false, err
	}
	stepMap := make(map[string]*state.WorkflowStep, len(stepRows))
	for _, row := range stepRows {
		stepMap[row.StepName] = row
	}
	if len(items) == 1 {
		action := spec.ForEach.Action.Step(items[0].StepName)
		return o.executeStep(ctx, run, items[0], action, stepMap, workflowSpecs)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, len(items))
	for _, item := range items {
		item := item
		wg.Go(func() {
			action := spec.ForEach.Action.Step(item.StepName)
			if _, err := o.executeStep(ctx, run, item, action, stepMap, workflowSpecs); err != nil &&
				!errors.Is(err, state.ErrWorkflowGuardNotReady) &&
				!errors.Is(err, state.ErrWorkflowOutboundAttemptExpired) {
				errCh <- err
			}
		})
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		return false, err
	}
	// Re-read the batch after all admitted actions have finished. This fills
	// newly opened slots and finalizes failures only after in-flight items drain.
	return true, nil
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
