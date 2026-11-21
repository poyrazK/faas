package sched

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/tasktoolrehearsal"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

// InterruptHostedTaskWorker consumes the running instance as a one-shot
// interruption target. A failed or lost destroy is inconclusive and the
// owning rehearsal must dispose of the run.
func (e *Engine) InterruptHostedTaskWorker(ctx context.Context, in tasktoolrehearsal.WorkerInterruption) (tasktoolrehearsal.StoppedWorker, error) {
	var result tasktoolrehearsal.StoppedWorker
	for _, id := range []string{in.AccountID, in.RunID, in.TaskID, in.AppID, in.DeploymentID, in.InstanceID, in.NodeID, in.WakeID} {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed == uuid.Nil {
			return result, state.ErrInvalidArgument
		}
	}
	runID := uuid.MustParse(in.RunID)
	const role = "worker"
	if in.RunID != runID.String() || in.AppID != tasktoolrehearsal.WorkloadID(runID, role) || !toolpublication.ValidID(in.PlanSHA256) {
		return result, state.ErrConflict
	}
	reservations, ok := e.store.(interface {
		MatchHostedTaskRehearsalReservation(context.Context, string, string, string, time.Time) (bool, error)
	})
	if !ok {
		return result, state.ErrConflict
	}

	release := e.lockApp(in.AppID)
	defer release()
	age := time.Since(in.ObservedAt)
	if age < 0 || age >= tasktoolrehearsal.WorkerStopDeadline {
		return result, state.ErrConflict
	}
	admission, cancel := context.WithDeadline(ctx, in.ObservedAt.Add(tasktoolrehearsal.WorkerStopDeadline))
	defer cancel()
	app, err := e.store.AppByID(admission, in.AppID)
	if err != nil {
		return result, err
	}
	scenario := strings.ReplaceAll(in.RunID, "-", "")
	if app.ID != in.AppID || app.AccountID != in.AccountID || app.Status != state.AppActive || app.Visibility != api.AppVisibilityInternal ||
		app.PreviewPrState != state.PreviewPrStateOpen || app.PreviewPrNumber != 0 ||
		app.Slug != "task-"+scenario[:24]+"-"+role || app.PreviewOfSlug != "task-"+scenario ||
		app.PreviewExpiresAt == nil || !app.PreviewExpiresAt.After(time.Now()) {
		return result, state.ErrConflict
	}
	reservedAt := app.PreviewExpiresAt.Add(-tasktoolrehearsal.WorkloadLease)
	matches, err := reservations.MatchHostedTaskRehearsalReservation(admission, in.AccountID, in.RunID, in.PlanSHA256, reservedAt)
	if err != nil {
		return result, err
	}
	if !matches {
		return result, state.ErrConflict
	}
	member, err := e.store.ScenarioTestMemberByApp(admission, in.AppID)
	if err != nil {
		return result, err
	}
	if member.AccountID != in.AccountID || member.AppID != in.AppID || member.RunID != scenario ||
		member.Workload != role || member.NetworkMode != state.ScenarioNetworkContained ||
		member.WebhookMode != state.ScenarioWebhookCaptureMode {
		return result, state.ErrConflict
	}
	dep, err := e.store.DeploymentByID(admission, in.DeploymentID)
	if err != nil {
		return result, err
	}
	if dep.ID != in.DeploymentID || dep.AppID != in.AppID || dep.Scope != api.DefaultEnvScope || dep.Status != state.DeployLive ||
		!dep.TrafficPercentExplicit || dep.TrafficPercent != 0 || dep.RootfsKey == "" {
		return result, state.ErrConflict
	}
	ins, err := e.store.InstanceByID(admission, in.InstanceID)
	if err != nil {
		return result, err
	}
	if ins.ID != in.InstanceID || ins.AppID != in.AppID || ins.DeploymentID != in.DeploymentID || ins.NodeID != in.NodeID ||
		ins.WakeID != in.WakeID || ins.Mode != string(state.InstanceModeWorker) || ins.State != string(state.StateRunning) ||
		ins.Netns == "" || e.workerStopping(ins.ID) {
		return result, state.ErrConflict
	}
	snapshot, err := e.vmm.Stats(admission, ins.NodeID)
	if err != nil {
		return result, err
	}
	if !residentHostedTaskWorker(snapshot, ins.ID) || !app.PreviewExpiresAt.After(time.Now()) {
		return result, state.ErrConflict
	}
	if err := admission.Err(); err != nil {
		return result, err
	}
	if err := updateInstanceStateCAS(admission, e.store, ins.ID, string(state.StateRunning), string(state.StateDraining)); err != nil {
		return result, err
	}
	e.recordCommittedInstanceTransition(admission, ins, state.StateRunning, state.StateDraining, ins.AppID, "hosted_task_rehearsal_interruption", in.TaskID)
	if err := admission.Err(); err != nil {
		return result, err
	}
	if err := e.timedDestroy(ctx, ins.NodeID, ins.ID, DestroyTimeout); err != nil {
		return result, fmt.Errorf("interrupt rehearsal worker: %w", err)
	}

	commit, stop := context.WithTimeout(context.WithoutCancel(ctx), tasktoolrehearsal.WorkerStopDeadline)
	defer stop()
	if err := updateInstanceStateCAS(commit, e.store, ins.ID, string(state.StateDraining), string(state.StateStopped)); err != nil {
		return result, err
	}
	e.releaseHostPortLeases(commit, ins.NodeID, ins.ID)
	e.recordCommittedInstanceTransition(commit, ins, state.StateDraining, state.StateStopped, ins.AppID, "hosted_task_rehearsal_interruption", in.TaskID)
	e.ledger.Release(ins.ID)
	e.scheduleWorkerAppReconcile(commit, ins.AppID)
	return tasktoolrehearsal.StoppedWorker{InstanceID: ins.ID, DeploymentID: ins.DeploymentID}, nil
}

func residentHostedTaskWorker(snapshot *StatsSnapshot, instance string) bool {
	if snapshot == nil {
		return false
	}
	for _, stat := range snapshot.Instances {
		if stat.InstanceID == instance && stat.ResidentBytes != nil && *stat.ResidentBytes > 0 {
			return true
		}
	}
	return false
}
