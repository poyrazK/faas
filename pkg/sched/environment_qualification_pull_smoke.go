// adr: 20261022155812
package sched

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// probeQualificationPullSmoke arms the reviewed message for this exact
// candidate execution, then waits for the candidate's own pull consumer to
// lease and settle it through the node-local qualification proxy. No
// invocation, trigger record or customer queue row is created; an ack is the
// handler-completion evidence and a nack fails the smoke.
func (e *Engine) probeQualificationPullSmoke(ctx context.Context, request state.EnvironmentWorkloadQualificationRequest,
	instance state.Instance, message state.EnvironmentQualificationQueueSmokeMessage) (EnvironmentQualificationQueueProbeResult, error) {
	var result EnvironmentQualificationQueueProbeResult
	store, ok := e.store.(state.EnvironmentQualificationPullSmokeStore)
	if !ok || message.Mode != "pull" {
		return result, state.ErrEnvironmentWorkloadPreparationUnavailable
	}
	smoke := state.EnvironmentQualificationPullSmoke{RequestID: request.ID, Attempt: request.Attempt, InstanceID: instance.ID,
		Binding: message.BindingName, MessageID: qualificationQueueSmokeMessageID(request, message.BindingID),
		Deadline: time.Now().Add(api.EnvironmentGitOpsQualificationPullSmokeWindow)}
	if err := store.ArmEnvironmentQualificationPullSmoke(ctx, smoke); err != nil {
		return result, fmt.Errorf("arm pull smoke: %w", err)
	}
	ticker := time.NewTicker(api.EnvironmentGitOpsQualificationPullSmokePoll)
	defer ticker.Stop()
	for {
		armed, err := store.EnvironmentQualificationPullSmoke(ctx, request.ID, request.Attempt, instance.ID, message.BindingName)
		if err != nil {
			return result, fmt.Errorf("read pull smoke: %w", err)
		}
		switch armed.Outcome {
		case state.PullSmokeAcked:
			return EnvironmentQualificationQueueProbeResult{StatusCode: http.StatusOK, Completed: true}, nil
		case state.PullSmokeNacked:
			return EnvironmentQualificationQueueProbeResult{StatusCode: http.StatusOK, Completed: true, Failed: true}, nil
		}
		if !time.Now().Before(armed.Deadline) {
			return result, fmt.Errorf("candidate did not settle its pull smoke (%d deliveries): %w", armed.Deliveries, state.ErrConflict)
		}
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case <-ticker.C:
		}
	}
}
