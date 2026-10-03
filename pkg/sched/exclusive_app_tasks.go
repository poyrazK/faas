package sched

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/exclusivework"
	"github.com/onebox-faas/faas/pkg/state"
)

const exclusiveAppTaskIncarnationPrefix = "app-task-operation/"

type exclusiveAppTaskWorkRequest struct {
	Kind         string                   `json:"kind"`
	DeploymentID string                   `json:"deployment_id"`
	CronID       string                   `json:"cron_id"`
	Task         api.CreateAppTaskRequest `json:"task"`
}

func (d *Drain) dispatchExclusiveAppTaskOperation(ctx context.Context, owners state.ExclusiveWorkStore, op state.ExclusiveOperation) string {
	var accepted exclusiveAppTaskWorkRequest
	if err := json.Unmarshal(op.Request, &accepted); err != nil ||
		(accepted.Kind != "app_task" && accepted.Kind != "command_cron") ||
		(accepted.Kind == "app_task" && accepted.DeploymentID == "") ||
		(accepted.Kind == "command_cron" && accepted.CronID == "") {
		_ = owners.FailPendingExclusiveOperation(ctx, op.AccountID, op.ID, "accepted app task request is invalid")
		return "failed"
	}
	if d.appTasks == nil {
		_ = owners.FailPendingExclusiveOperation(ctx, op.AccountID, op.ID, "app task execution is unavailable")
		return "failed"
	}
	tasks, ok := d.store.(state.AppTaskStore)
	if !ok {
		_ = owners.FailPendingExclusiveOperation(ctx, op.AccountID, op.ID, "app task store is unavailable")
		return "failed"
	}
	createTask := func(claim exclusivework.Claim) (state.AppTask, error) {
		if accepted.Kind == "command_cron" {
			cronTasks, ok := d.store.(state.ExclusiveCommandCronTaskStore)
			if !ok {
				return state.AppTask{}, errors.New("exclusive command-cron task store is unavailable")
			}
			return cronTasks.CreateExclusiveCommandCronAppTask(ctx, op.AccountID, op.AppID, op.ID, claim.Generation, accepted.CronID, d.now().UTC())
		}
		resolved, problem := accepted.Task.Resolve()
		if problem != nil {
			return state.AppTask{}, state.ErrAppTaskInvalid
		}
		return tasks.CreateAppTask(ctx, state.CreateAppTaskParams{
			AccountID: op.AccountID, AppID: op.AppID, DeploymentID: accepted.DeploymentID,
			ExclusiveOperationID: op.ID, ExclusiveGeneration: claim.Generation,
			Kind: state.AppTaskKindManual, Command: resolved.Command, CommandShell: resolved.CommandShell,
			TimeoutSeconds: resolved.TimeoutSeconds, MaxOutputBytes: resolved.MaxOutputBytes, CreatedAt: d.now().UTC(),
		})
	}
	app, err := d.store.AppByID(ctx, op.AppID)
	if err != nil || app.AccountID != op.AccountID || app.Status == state.AppDeleted {
		_ = owners.FailPendingExclusiveOperation(ctx, op.AccountID, op.ID, "application is unavailable")
		return "failed"
	}
	claim, err := owners.ClaimExclusiveOperation(ctx, op.AccountID, op.ID, exclusiveAppTaskIncarnationPrefix+op.ID)
	if err != nil {
		if errors.Is(err, state.ErrQuotaExceeded) {
			_ = owners.DeferPendingExclusiveOperation(ctx, op.AccountID, op.ID, "account async capacity is full")
			return "retry"
		}
		if errors.Is(err, exclusivework.ErrBusy) {
			return "retry"
		}
		return "failed"
	}
	prior, err := tasks.ListAppTasksByExclusiveOperation(ctx, op.AccountID, op.ID)
	if err != nil {
		_ = owners.RetryExclusiveOperation(ctx, claim, "could not inspect prior app task incarnations")
		return "retry"
	}
	for _, task := range prior {
		if task.ExclusiveGeneration >= claim.Generation || task.Status.Terminal() {
			continue
		}
		d.cancelExclusiveAppTask(ctx, tasks, task)
	}
	task, err := createTask(claim)
	if err != nil {
		if errors.Is(err, exclusivework.ErrStaleOwner) {
			return "lost_owner"
		}
		if errors.Is(err, state.ErrAppTaskInvalid) || errors.Is(err, state.ErrAppTaskDeploymentUnavailable) ||
			errors.Is(err, state.ErrAppTaskCronDisabled) || errors.Is(err, state.ErrAppTaskCronSuspended) ||
			errors.Is(err, state.ErrAppTaskCronOverlap) || errors.Is(err, state.ErrNotFound) {
			_ = owners.FailExclusiveOperation(ctx, claim, "deployment-attached task could not be created")
			return "failed"
		}
		_ = owners.RetryExclusiveOperation(ctx, claim, "deployment-attached task creation is temporarily unavailable")
		return "retry"
	}
	go d.watchExclusiveAppTaskOperation(ctx, owners, tasks, claim, task)
	return "completed"
}

func (d *Drain) cancelExclusiveAppTask(ctx context.Context, tasks state.AppTaskStore, task state.AppTask) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if _, err := tasks.RequestAppTaskCancellation(cleanupCtx, task.AccountID, task.AppID, task.ID, d.now().UTC()); err != nil && !errors.Is(err, state.ErrNotFound) {
		d.log.WarnContext(ctx, "exclusive app task could not cancel stale task", "operation_id", task.ExclusiveOperationID, "task_id", task.ID, "err", err)
	}
}

func (d *Drain) watchExclusiveAppTaskOperation(ctx context.Context, owners state.ExclusiveWorkStore, tasks state.AppTaskStore, initial exclusivework.Claim, task state.AppTask) {
	claim := initial
	var claimMu sync.Mutex
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopRenewal := make(chan struct{})
	renewalDone := make(chan struct{})
	renewalFailure := make(chan error, 1)
	interval := initial.ExpiresAt.Sub(d.now()) / 3
	if interval < 250*time.Millisecond {
		interval = 250 * time.Millisecond
	}
	go func() {
		defer close(renewalDone)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stopRenewal:
				return
			case <-workerCtx.Done():
				return
			case <-ticker.C:
				claimMu.Lock()
				current := claim
				claimMu.Unlock()
				next, err := owners.RenewExclusiveOperation(workerCtx, current)
				if err != nil {
					select {
					case renewalFailure <- err:
					default:
					}
					cancel()
					return
				}
				claimMu.Lock()
				claim = next
				claimMu.Unlock()
			}
		}
	}()

	poll := time.NewTicker(min(interval, time.Second))
	defer poll.Stop()
	for {
		select {
		case err := <-renewalFailure:
			if !errors.Is(err, exclusivework.ErrStaleOwner) {
				d.log.WarnContext(ctx, "exclusive app task ownership renewal failed", "operation_id", initial.OperationID, "err", err)
			}
			d.cancelExclusiveAppTask(ctx, tasks, task)
			return
		case <-workerCtx.Done():
			return
		case <-poll.C:
			currentTask, err := tasks.AppTaskByID(workerCtx, task.AccountID, task.AppID, task.ID)
			if err != nil {
				if errors.Is(err, state.ErrNotFound) {
					close(stopRenewal)
					<-renewalDone
					claimMu.Lock()
					current := claim
					claimMu.Unlock()
					_ = owners.FailExclusiveOperation(ctx, current, "managed app task disappeared")
					return
				}
				d.log.WarnContext(ctx, "exclusive app task lookup failed", "operation_id", initial.OperationID, "err", err)
				continue
			}
			if !currentTask.Status.Terminal() {
				continue
			}
			close(stopRenewal)
			<-renewalDone
			claimMu.Lock()
			current := claim
			claimMu.Unlock()
			result, _ := json.Marshal(map[string]any{
				"app_task_id": currentTask.ID, "status": currentTask.Status, "exit_code": currentTask.ExitCode,
			})
			if currentTask.Status == state.AppTaskSucceeded {
				if err := owners.CommitExclusiveOperation(ctx, current, result, nil); err != nil && !errors.Is(err, exclusivework.ErrStaleOwner) {
					d.log.WarnContext(ctx, "exclusive app task result commit failed", "operation_id", current.OperationID, "err", err)
				}
			} else {
				reason := fmt.Sprintf("deployment-attached task ended with status %s", currentTask.Status)
				if currentTask.FailureCode != nil {
					reason += " (" + *currentTask.FailureCode + ")"
				}
				_ = owners.FailExclusiveOperation(ctx, current, reason)
			}
			return
		}
	}
}
