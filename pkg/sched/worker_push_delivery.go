package sched

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// workerPushDelivery reports whether a worker-mode app has a push queue
// binding. Push deliveries are private HTTP POSTs to the worker's port, so
// vmmd accepts a bound server from such a worker at startup instead of an
// execution_mode_conflict. A disabled binding keeps the app's delivery
// shape; a retired one no longer delivers.
func (e *Engine) workerPushDelivery(ctx context.Context, app state.App) (bool, error) {
	if executionModeForApp(app) != api.ExecutionModeWorker {
		return false, nil
	}
	bindings, err := e.store.ListQueueBindingsForApp(ctx, app.AccountID, app.ID)
	if err != nil {
		return false, fmt.Errorf("list queue bindings: %w", err)
	}
	for _, binding := range bindings {
		if binding.Mode == "push" {
			return true, nil
		}
	}
	return false, nil
}

// qualificationWorkerPushDelivery is workerPushDelivery for a qualification
// candidate: its frozen review, not live intent, names its push consumers.
// The smoke visitor validates those bindings; the boot only needs their shape.
func qualificationWorkerPushDelivery(request state.EnvironmentWorkloadQualificationRequest) bool {
	if request.ExecutionMode != api.ExecutionModeWorker {
		return false
	}
	for _, binding := range request.FrozenInputs.QueueBindings {
		if binding.Contract.Mode == "push" {
			return true
		}
	}
	return false
}
