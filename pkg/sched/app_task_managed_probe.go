package sched

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func (c *AppTaskCoordinator) executeTaskCommand(ctx context.Context, session AppTaskSession, task state.AppTask, interactive *AppTaskAttachSpec) (AppTaskOutcome, error) {
	if task.BindingVerification != nil && task.BindingVerification.ManagedService != nil {
		store, ok := c.store.(state.ManagedServiceProbeStore)
		if !ok || state.ManagedServiceProbeCurrent(ctx, store, task) != nil {
			return AppTaskOutcome{}, state.ErrAppTaskDeploymentUnavailable
		}
	}
	return session.Execute(ctx, AppTaskExecuteRequest{Command: state.AppTaskGuestCommand(task), CommandShell: task.CommandShell,
		Timeout: time.Duration(task.TimeoutSeconds) * time.Second, MaxOutputBytes: task.MaxOutputBytes, Interactive: interactive})
}
