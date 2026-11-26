package state_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apptaskproto"
	"github.com/onebox-faas/faas/pkg/dataapiinspectioncommand"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDataAPIInspectionExecutionMem(t *testing.T) {
	dataAPIInspectionExecutionSuite(t, state.NewMemStore())
}
func TestDataAPIInspectionExecutionPG(t *testing.T) {
	store, _, _ := pgStoreWithPool(t)
	dataAPIInspectionExecutionSuite(t, store)
}

func dataAPIInspectionExecutionSuite(t *testing.T, store state.Store) {
	t.Helper()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, uuid.NewString()+"@inspection.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "inspect-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployPending, Scope: "default", ImageDigest: "sha256:" + strings.Repeat("1", 64), CreatedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.SetDeploymentRootfs(ctx, dep.ID, "/test/rootfs", "test/rootfs", 4096); err != nil {
		t.Fatal(err)
	}
	if err = store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	tasks, evidence := store.(state.AppTaskStore), store.(state.DataAPIInspectionExecutionStore)
	now := time.Now().UTC().Truncate(time.Microsecond)
	params := state.CreateAppTaskParams{AccountID: account.ID, AppID: app.ID, DeploymentID: dep.ID, Kind: state.AppTaskKindManual, Command: []string{dataapiinspectioncommand.Command, "notes", strings.Repeat("a", 64)}, CreatedAt: now, MaxOutputBytes: 4096}
	task, err := tasks.CreateAppTask(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := tasks.ClaimNextAppTask(ctx, "inspection-test", now, time.Minute)
	if err != nil || claimed.ID != task.ID || claimed.LeaseToken == nil {
		t.Fatalf("claim: %+v %v", claimed, err)
	}
	if _, err := tasks.MarkAppTaskRunning(ctx, task.ID, *claimed.LeaseToken, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	zero := 0
	complete := state.CompleteAppTaskParams{ID: task.ID, LeaseToken: *claimed.LeaseToken, Status: state.AppTaskSucceeded, ExitCode: &zero, StdoutTail: "inspection output", FinishedAt: now.Add(2 * time.Second), DataAPIInspectionProtocol: apptaskproto.DataAPIInspectionVersion}
	wrong := complete
	wrong.DataAPIInspectionProtocol++
	if _, err := tasks.CompleteAppTask(ctx, wrong); !errors.Is(err, state.ErrAppTaskInvalid) {
		t.Fatalf("invalid protocol: %v", err)
	}
	wrong = complete
	wrong.OutputTruncated = true
	if _, err := tasks.CompleteAppTask(ctx, wrong); !errors.Is(err, state.ErrAppTaskInvalid) {
		t.Fatalf("truncated output: %v", err)
	}
	if _, err := evidence.DataAPIInspectionExecution(ctx, account.ID, app.ID, task.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("evidence before completion: %v", err)
	}
	if _, err := tasks.CompleteAppTask(ctx, complete); err != nil {
		t.Fatal(err)
	}
	got, err := evidence.DataAPIInspectionExecution(ctx, account.ID, app.ID, task.ID)
	if err != nil || got.TaskID != task.ID || got.DeploymentID != dep.ID || got.ProtocolVersion != apptaskproto.DataAPIInspectionVersion || got.StdoutSHA256 != sha256.Sum256([]byte(complete.StdoutTail)) {
		t.Fatalf("evidence: %+v %v", got, err)
	}
	if _, err := evidence.DataAPIInspectionExecution(ctx, uuid.NewString(), app.ID, task.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account evidence: %v", err)
	}
	params.CreatedAt = now.Add(3 * time.Second)
	legacy, err := tasks.CreateAppTask(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err = tasks.ClaimNextAppTask(ctx, "inspection-test", params.CreatedAt, time.Minute)
	if err != nil || claimed.ID != legacy.ID || claimed.LeaseToken == nil {
		t.Fatalf("claim legacy: %+v %v", claimed, err)
	}
	if _, err := tasks.MarkAppTaskRunning(ctx, legacy.ID, *claimed.LeaseToken, now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	complete.ID, complete.LeaseToken, complete.FinishedAt, complete.DataAPIInspectionProtocol = legacy.ID, *claimed.LeaseToken, now.Add(5*time.Second), 0
	if _, err := tasks.CompleteAppTask(ctx, complete); err != nil {
		t.Fatal(err)
	}
	if _, err := evidence.DataAPIInspectionExecution(ctx, account.ID, app.ID, legacy.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("legacy evidence: %v", err)
	}
	params.CreatedAt = now.Add(6 * time.Second)
	cancelled, err := tasks.CreateAppTask(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err = tasks.ClaimNextAppTask(ctx, "inspection-test", params.CreatedAt, time.Minute)
	if err != nil || claimed.ID != cancelled.ID || claimed.LeaseToken == nil {
		t.Fatalf("claim cancelled: %+v %v", claimed, err)
	}
	if _, err := tasks.MarkAppTaskRunning(ctx, cancelled.ID, *claimed.LeaseToken, now.Add(7*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.RequestAppTaskCancellation(ctx, account.ID, app.ID, cancelled.ID, now.Add(8*time.Second)); err != nil {
		t.Fatal(err)
	}
	complete.ID, complete.LeaseToken, complete.FinishedAt, complete.DataAPIInspectionProtocol = cancelled.ID, *claimed.LeaseToken, now.Add(9*time.Second), apptaskproto.DataAPIInspectionVersion
	if _, err := tasks.CompleteAppTask(ctx, complete); !errors.Is(err, state.ErrAppTaskCancellationPending) {
		t.Fatalf("completion after cancellation: %v", err)
	}
	if _, err := evidence.DataAPIInspectionExecution(ctx, account.ID, app.ID, cancelled.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cancelled evidence: %v", err)
	}
}
