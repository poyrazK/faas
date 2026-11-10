package imaged

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// A release task the platform could not run or observe fails the candidate
// as release_infrastructure_failed with whycopy guidance; the command's own
// failure stays release_command_failed. The live deployment keeps serving
// either way.
func TestReleaseTaskFailureNamesWhoFailed(t *testing.T) {
	exitOne := 1
	tests := []struct {
		name     string
		status   state.AppTaskStatus
		code     string
		message  string
		exitCode *int
		wantCode string
		wantHint bool
	}{
		{name: "environment could not be prepared", status: state.AppTaskFailed, code: "restore_failed",
			message: "app task environment could not be prepared", wantCode: api.CodeReleaseInfrastructure, wantHint: true},
		{name: "result channel closed", status: state.AppTaskFailed, code: "execution_transport_failed",
			message: "app task result channel closed unexpectedly", wantCode: api.CodeReleaseInfrastructure, wantHint: true},
		{name: "command exited non-zero", status: state.AppTaskFailed, code: "command_failed",
			message: "command exited unsuccessfully", exitCode: &exitOne, wantCode: api.CodeReleaseCommandFailed},
		{name: "command timed out", status: state.AppTaskTimedOut, code: "timeout",
			message: "app task command timed out", wantCode: api.CodeReleaseCommandFailed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fx := seedReleaseGate(t, true)
			ctx := context.Background()
			if err := fx.handler.handoffSnapshotPrime(ctx, fx.app, fx.candidate); err != nil {
				t.Fatalf("handoffSnapshotPrime: %v", err)
			}
			task, _ := fx.store.ReleaseAppTaskByDeployment(ctx, fx.candidate.ID)
			claimedAt := time.Now().UTC().Add(time.Second)
			claimed, err := fx.store.ClaimNextAppTask(ctx, "schedd", claimedAt, time.Minute)
			if err != nil {
				t.Fatalf("ClaimNextAppTask: %v", err)
			}
			if _, err := fx.store.MarkAppTaskRunning(ctx, task.ID, *claimed.LeaseToken, claimedAt.Add(time.Second)); err != nil {
				t.Fatalf("MarkAppTaskRunning: %v", err)
			}
			code, message := tc.code, tc.message
			task, err = fx.store.CompleteAppTask(ctx, state.CompleteAppTaskParams{
				ID: task.ID, LeaseToken: *claimed.LeaseToken, Status: tc.status,
				ExitCode: tc.exitCode, FailureCode: &code, FailureMessage: &message,
				FinishedAt: claimedAt.Add(2 * time.Second),
			})
			if err != nil {
				t.Fatalf("CompleteAppTask: %v", err)
			}
			if err := fx.handler.HandleNotification(ctx, fx.terminalNotification(t, task)); err != nil {
				t.Fatalf("HandleNotification(app_task_changed): %v", err)
			}
			candidate, _ := fx.store.DeploymentByID(ctx, fx.candidate.ID)
			if candidate.Status != state.DeployFailed || candidate.ErrorCode != tc.wantCode {
				t.Fatalf("candidate = %s/%q, want failed/%q", candidate.Status, candidate.ErrorCode, tc.wantCode)
			}
			if hasHint := candidate.ErrorHint != ""; hasHint != tc.wantHint {
				t.Fatalf("candidate hint = %q, want present=%v", candidate.ErrorHint, tc.wantHint)
			}
			previous, _ := fx.store.DeploymentByID(ctx, fx.previous.ID)
			if previous.Status != state.DeployLive {
				t.Fatalf("previous deployment status = %q, want live", previous.Status)
			}
		})
	}
}

// A release task whose worker lease expired after its command started has an
// unknown outcome (ADR-1448 amendment 1). It is the platform's failure, not
// the command's, and the guidance names `gregale release resolve`, because
// the next release command waits for that check.
func TestReleaseTaskLeaseExpiredIsUncertainNotCommandFailure(t *testing.T) {
	fx := seedReleaseGate(t, true)
	ctx := context.Background()
	if err := fx.handler.handoffSnapshotPrime(ctx, fx.app, fx.candidate); err != nil {
		t.Fatalf("handoffSnapshotPrime: %v", err)
	}
	task, _ := fx.store.ReleaseAppTaskByDeployment(ctx, fx.candidate.ID)
	claimedAt := time.Now().UTC().Add(time.Second)
	claimed, err := fx.store.ClaimNextAppTask(ctx, "schedd", claimedAt, time.Minute)
	if err != nil {
		t.Fatalf("ClaimNextAppTask: %v", err)
	}
	if _, err := fx.store.MarkAppTaskRunning(ctx, task.ID, *claimed.LeaseToken, claimedAt.Add(time.Second)); err != nil {
		t.Fatalf("MarkAppTaskRunning: %v", err)
	}
	if _, err := fx.store.SweepExpiredAppTasks(ctx, claimedAt.Add(2*time.Minute)); err != nil {
		t.Fatalf("SweepExpiredAppTasks: %v", err)
	}
	task, _ = fx.store.ReleaseAppTaskByDeployment(ctx, fx.candidate.ID)
	if task.Status != state.AppTaskFailed || task.FailureCode == nil || *task.FailureCode != "lease_expired" {
		t.Fatalf("swept task = %s/%v, want failed/lease_expired", task.Status, task.FailureCode)
	}
	if err := fx.handler.HandleNotification(ctx, fx.terminalNotification(t, task)); err != nil {
		t.Fatalf("HandleNotification(app_task_changed): %v", err)
	}
	candidate, _ := fx.store.DeploymentByID(ctx, fx.candidate.ID)
	if candidate.Status != state.DeployFailed || candidate.ErrorCode != api.CodeReleaseInfrastructure {
		t.Fatalf("candidate = %s/%q, want failed/%q", candidate.Status, candidate.ErrorCode, api.CodeReleaseInfrastructure)
	}
	if !strings.Contains(candidate.ErrorWhy, "unknown") || !strings.Contains(candidate.ErrorFix, "gregale release resolve") {
		t.Fatalf("candidate why/fix = %q / %q, want the unknown outcome and the resolve command", candidate.ErrorWhy, candidate.ErrorFix)
	}
	previous, _ := fx.store.DeploymentByID(ctx, fx.previous.ID)
	if previous.Status != state.DeployLive {
		t.Fatalf("previous deployment status = %q, want live", previous.Status)
	}
}
