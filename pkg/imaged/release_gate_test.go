package imaged

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

type releaseGateFixture struct {
	store     *state.MemStore
	handler   *Handler
	notifier  *fakeNotifier
	account   state.Account
	app       state.App
	previous  state.Deployment
	candidate state.Deployment
}

func seedReleaseGate(t *testing.T, enabled bool) releaseGateFixture {
	return seedReleaseGateWithIntent(t, enabled, true)
}

func seedReleaseGateWithIntent(t *testing.T, enabled, hasReleaseCommand bool) releaseGateFixture {
	t.Helper()
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "release-gate@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID: account.ID, Slug: "release-gate", RAMMB: 256, Status: state.AppActive,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	previous, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:previous",
		Status: state.DeploySnapshotting,
	})
	if err != nil {
		t.Fatalf("CreateDeployment(previous): %v", err)
	}
	if err := store.MarkDeploymentLive(ctx, previous.ID); err != nil {
		t.Fatalf("MarkDeploymentLive(previous): %v", err)
	}
	candidateParams := state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:candidate",
		Status: state.DeploySnapshotting,
	}
	if hasReleaseCommand {
		candidateParams.ReleaseCommand = []string{"bin/migrate", "--safe"}
	}
	candidate, err := store.CreateDeployment(ctx, candidateParams)
	if err != nil {
		t.Fatalf("CreateDeployment(candidate): %v", err)
	}
	if err := store.SetDeploymentRootfs(ctx, candidate.ID, "/tmp/release-gate.ext4", "apps/release-gate/candidate.ext4", 4096); err != nil {
		t.Fatalf("SetDeploymentRootfs(candidate): %v", err)
	}
	candidate, err = store.DeploymentByID(ctx, candidate.ID)
	if err != nil {
		t.Fatalf("DeploymentByID(candidate): %v", err)
	}
	notifier := &fakeNotifier{}
	handler := New(store, notifier, nil, nil, "", t.TempDir(), silentLogger()).WithReleasePhaseEnabled(enabled)
	return releaseGateFixture{
		store: store, handler: handler, notifier: notifier, account: account,
		app: app, previous: previous, candidate: candidate,
	}
}

func (fx releaseGateFixture) terminalNotification(t *testing.T, task state.AppTask) db.Notification {
	t.Helper()
	payload, err := json.Marshal(db.AppTaskChangedPayload{
		AccountID: task.AccountID, AppID: task.AppID, DeploymentID: task.DeploymentID,
		TaskID: task.ID, Kind: string(task.Kind), Status: string(task.Status),
	})
	if err != nil {
		t.Fatalf("marshal task notification: %v", err)
	}
	return db.Notification{Channel: db.NotifyAppTaskChanged, Payload: string(payload)}
}

func TestReleaseGateAdmitsOneTaskAndWaitsBeforeBoot(t *testing.T) {
	fx := seedReleaseGate(t, true)
	ctx := context.Background()
	if err := fx.handler.handoffSnapshotPrime(ctx, fx.app, fx.candidate); err != nil {
		t.Fatalf("handoffSnapshotPrime: %v", err)
	}
	if findNotify(fx.notifier, db.NotifySnapshotPrime) != nil {
		t.Fatal("snapshot_prime emitted before release task completed")
	}
	task, err := fx.store.ReleaseAppTaskByDeployment(ctx, fx.candidate.ID)
	if err != nil {
		t.Fatalf("ReleaseAppTaskByDeployment: %v", err)
	}
	if task.Status != state.AppTaskQueued || task.CommandShell || len(task.Command) != 2 || task.Command[0] != "bin/migrate" {
		t.Fatalf("release task = %+v", task)
	}
	if err := fx.handler.handoffSnapshotPrime(ctx, fx.app, fx.candidate); err != nil {
		t.Fatalf("replayed handoffSnapshotPrime: %v", err)
	}
	tasks, err := fx.store.ListAppTasks(ctx, fx.account.ID, fx.app.ID, 10, 0)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("release tasks after replay = %+v, err=%v", tasks, err)
	}
}

func TestReleaseGateSuccessResumesSnapshotPrime(t *testing.T) {
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
	exitZero := 0
	task, err = fx.store.CompleteAppTask(ctx, state.CompleteAppTaskParams{
		ID: task.ID, LeaseToken: *claimed.LeaseToken, Status: state.AppTaskSucceeded,
		ExitCode: &exitZero, FinishedAt: claimedAt.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatalf("CompleteAppTask: %v", err)
	}
	if err := fx.handler.HandleNotification(ctx, fx.terminalNotification(t, task)); err != nil {
		t.Fatalf("HandleNotification(app_task_changed): %v", err)
	}
	if findNotify(fx.notifier, db.NotifySnapshotPrime) == nil {
		t.Fatal("release success did not resume snapshot_prime")
	}
	deployment, _ := fx.store.DeploymentByID(ctx, fx.candidate.ID)
	if deployment.Status != state.DeploySnapshotting {
		t.Fatalf("candidate status = %q, want snapshotting until readiness", deployment.Status)
	}
}

func TestReleaseGateFailurePreservesPreviousLiveDeployment(t *testing.T) {
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
	exitOne := 1
	code, message := "exit_nonzero", "release command exited unsuccessfully"
	task, err = fx.store.CompleteAppTask(ctx, state.CompleteAppTaskParams{
		ID: task.ID, LeaseToken: *claimed.LeaseToken, Status: state.AppTaskFailed,
		ExitCode: &exitOne, FailureCode: &code, FailureMessage: &message,
		FinishedAt: claimedAt.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatalf("CompleteAppTask: %v", err)
	}
	if err := fx.handler.HandleNotification(ctx, fx.terminalNotification(t, task)); err != nil {
		t.Fatalf("HandleNotification(app_task_changed): %v", err)
	}
	if findNotify(fx.notifier, db.NotifySnapshotPrime) != nil {
		t.Fatal("failed release task emitted snapshot_prime")
	}
	candidate, _ := fx.store.DeploymentByID(ctx, fx.candidate.ID)
	if candidate.Status != state.DeployFailed || candidate.ErrorCode != api.CodeReleaseCommandFailed {
		t.Fatalf("candidate after release failure = %+v", candidate)
	}
	previous, _ := fx.store.DeploymentByID(ctx, fx.previous.ID)
	if previous.Status != state.DeployLive {
		t.Fatalf("previous deployment status = %q, want live", previous.Status)
	}
}

func TestReleaseGateDisabledFailsCandidateWithReleaseIntent(t *testing.T) {
	fx := seedReleaseGate(t, false)
	ctx := context.Background()
	if err := fx.handler.handoffSnapshotPrime(ctx, fx.app, fx.candidate); err != nil {
		t.Fatalf("handoffSnapshotPrime: %v", err)
	}
	if findNotify(fx.notifier, db.NotifySnapshotPrime) != nil {
		t.Fatal("disabled release gate emitted snapshot_prime and skipped the command")
	}
	if _, err := fx.store.ReleaseAppTaskByDeployment(ctx, fx.candidate.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("disabled release gate task lookup = %v, want ErrNotFound", err)
	}
	candidate, err := fx.store.DeploymentByID(ctx, fx.candidate.ID)
	if err != nil {
		t.Fatalf("DeploymentByID(candidate): %v", err)
	}
	if candidate.Status != state.DeployFailed || candidate.ErrorCode != api.CodeReleasePhaseUnavailable {
		t.Fatalf("candidate after unavailable release phase = %+v", candidate)
	}
	if !strings.Contains(candidate.Error, "FAAS_RELEASE_PHASE_ENABLED=1") || !strings.Contains(candidate.Error, "FAAS_APP_TASK_DISPATCH=1") {
		t.Fatalf("unavailable release detail is not actionable: %q", candidate.Error)
	}
	previous, err := fx.store.DeploymentByID(ctx, fx.previous.ID)
	if err != nil || previous.Status != state.DeployLive {
		t.Fatalf("previous deployment = %+v, err=%v; want live", previous, err)
	}
}

func TestReleaseGateDisabledPreservesPrimePathWithoutReleaseIntent(t *testing.T) {
	fx := seedReleaseGateWithIntent(t, false, false)
	ctx := context.Background()
	if err := fx.handler.handoffSnapshotPrime(ctx, fx.app, fx.candidate); err != nil {
		t.Fatalf("handoffSnapshotPrime: %v", err)
	}
	if findNotify(fx.notifier, db.NotifySnapshotPrime) == nil {
		t.Fatal("deployment without release intent did not emit snapshot_prime")
	}
	if _, err := fx.store.ReleaseAppTaskByDeployment(ctx, fx.candidate.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("release task lookup = %v, want ErrNotFound", err)
	}
}

func TestReleaseGateDeploymentCancellationCancelsQueuedTask(t *testing.T) {
	fx := seedReleaseGate(t, true)
	ctx := context.Background()
	if err := fx.handler.handoffSnapshotPrime(ctx, fx.app, fx.candidate); err != nil {
		t.Fatalf("handoffSnapshotPrime: %v", err)
	}
	if _, _, err := fx.store.CancelDeploymentTx(ctx, fx.candidate.ID, "user:test", state.CancelReasonUser); err != nil {
		t.Fatalf("CancelDeploymentTx: %v", err)
	}
	task, err := fx.store.ReleaseAppTaskByDeployment(ctx, fx.candidate.ID)
	if err != nil {
		t.Fatalf("ReleaseAppTaskByDeployment: %v", err)
	}
	if task.Status != state.AppTaskCancelled || task.FinishedAt == nil {
		t.Fatalf("release task after deployment cancellation = %+v", task)
	}
}
