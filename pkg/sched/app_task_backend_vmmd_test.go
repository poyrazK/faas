// adr: 230
// adr: 462

package sched

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apptaskproto"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

type recordingRoutedAppTaskVMM struct {
	restoreNode, executeNode, destroyNode string
	restoreSpec                           AppTaskRestoreSpec
	executeRequest                        apptaskproto.Request
	streamed                              bool
}

func (r *recordingRoutedAppTaskVMM) RestoreAppTask(_ context.Context, nodeID string, spec AppTaskRestoreSpec) (*AppTaskRestoreOutcome, error) {
	r.restoreNode, r.restoreSpec = nodeID, spec
	return &AppTaskRestoreOutcome{Instance: spec.Instance, Method: fcvm.WakeColdBoot}, nil
}

func (r *recordingRoutedAppTaskVMM) ExecuteAppTask(_ context.Context, nodeID, _ string, request apptaskproto.Request) (apptaskproto.Result, error) {
	r.executeNode, r.executeRequest = nodeID, request
	exit := 0
	return apptaskproto.Result{Status: apptaskproto.StatusSucceeded, ExitCode: &exit, OutcomeCode: "accepted", Stdout: []byte("done\n")}, nil
}

func (r *recordingRoutedAppTaskVMM) ExecuteAppTaskWithOutput(ctx context.Context, nodeID, instance string, request apptaskproto.Request, _ apptaskproto.OutputReceiver) (apptaskproto.Result, error) {
	r.streamed = true
	return r.ExecuteAppTask(ctx, nodeID, instance, request)
}

func (r *recordingRoutedAppTaskVMM) Destroy(_ context.Context, nodeID, _ string) error {
	r.destroyNode = nodeID
	return nil
}

// adr: 385 — structured application outcomes survive the routed VM boundary.
func TestRoutedVmmdAppTaskBackendKeepsCommandOutOfRestoreAndPinsSession(t *testing.T) {
	router := &recordingRoutedAppTaskVMM{}
	var resolved AppTaskRestoreRequest
	releases := 0
	backend := NewRoutedVmmdAppTaskBackend(router, func(_ context.Context, request AppTaskRestoreRequest) (ResolvedAppTaskRuntime, error) {
		resolved = request
		return ResolvedAppTaskRuntime{NodeID: "node-a", Spec: AppTaskRestoreSpec{
			Instance: request.ID, DeploymentID: request.DeploymentID,
			App: AppSpec{Plan: api.PlanPro, AccountID: request.AccountID, AppID: request.AppID, DeploymentID: request.DeploymentID},
		}, release: func() { releases++ }}, nil
	})
	restore := AppTaskRestoreRequest{
		ID: "task-1", AccountID: "acct-1", AppID: "app-1", DeploymentID: "dep-1",
		Kind: state.AppTaskKindManual, ArtifactKey: "apps/app-1/dep-1.ext4", ImageDigest: "sha256:pinned",
	}
	session, err := backend.Restore(context.Background(), restore)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if resolved != restore || router.restoreNode != "node-a" || router.restoreSpec.Instance != "task-1" {
		t.Fatalf("restore resolution/router = %#v / %#v", resolved, router)
	}
	outcome, err := session.Execute(context.Background(), AppTaskExecuteRequest{
		Command: []string{"bin/migrate", "--once"}, Timeout: 3 * time.Second, MaxOutputBytes: 2048,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !router.streamed || router.executeNode != "node-a" || router.executeRequest.TaskID != "task-1" ||
		len(router.executeRequest.Command) != 2 || router.executeRequest.Command[0] != "bin/migrate" {
		t.Fatalf("execute route = %#v", router)
	}
	if outcome.Status != state.AppTaskSucceeded || outcome.StdoutTail != "done\n" || outcome.ExitCode == nil || *outcome.ExitCode != 0 || outcome.OutcomeCode != "accepted" {
		t.Fatalf("outcome = %#v", outcome)
	}
	if err := session.Destroy(context.Background()); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if err := session.Destroy(context.Background()); err != nil {
		t.Fatalf("second Destroy: %v", err)
	}
	if router.destroyNode != "node-a" || releases != 1 {
		t.Fatalf("destroy node/releases = %q/%d", router.destroyNode, releases)
	}
}

func TestRoutedVmmdAppTaskBackendCleansUpUnexpectedRestoreIdentity(t *testing.T) {
	router := &unexpectedAppTaskVMM{recordingRoutedAppTaskVMM: &recordingRoutedAppTaskVMM{}}
	backend := NewRoutedVmmdAppTaskBackend(router, func(context.Context, AppTaskRestoreRequest) (ResolvedAppTaskRuntime, error) {
		return ResolvedAppTaskRuntime{NodeID: "node-a", Spec: AppTaskRestoreSpec{Instance: "task-1", DeploymentID: "dep-1"}}, nil
	})
	if _, err := backend.Restore(context.Background(), AppTaskRestoreRequest{ID: "task-1", DeploymentID: "dep-1"}); err == nil {
		t.Fatal("Restore accepted an unexpected instance")
	}
	if router.destroyNode != "node-a" {
		t.Fatalf("cleanup node = %q", router.destroyNode)
	}
}

func TestResolveAppTaskRuntimeBuildsPinnedAppSpecAndRejectsDrift(t *testing.T) {
	store, account, app, deployment, tasks := newAppTaskCoordinatorFixture(t, 1, 30, 2048)
	engine := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	request := AppTaskRestoreRequest{
		ID: tasks[0].ID, AccountID: account.ID, AppID: app.ID, DeploymentID: deployment.ID,
		Kind: tasks[0].Kind, DeploymentScope: tasks[0].DeploymentScope,
		ArtifactKey: tasks[0].ArtifactKey, ImageDigest: tasks[0].ImageDigest,
	}
	resolved, err := engine.ResolveAppTaskRuntime(context.Background(), request)
	if err != nil {
		t.Fatalf("ResolveAppTaskRuntime: %v", err)
	}
	if resolved.NodeID == "" || resolved.Spec.Instance != tasks[0].ID || resolved.Spec.DeploymentID != deployment.ID {
		t.Fatalf("resolved identity = %#v", resolved)
	}
	defer resolved.release()
	if resolved.Spec.App.AccountID != account.ID || resolved.Spec.App.AppID != app.ID ||
		resolved.Spec.App.DeploymentID != deployment.ID || resolved.Spec.App.LayerKey != tasks[0].ArtifactKey ||
		resolved.Spec.App.BaseKey == "" || resolved.Spec.App.Runtime != app.Runtime {
		t.Fatalf("resolved AppSpec = %#v", resolved.Spec.App)
	}

	drifted := request
	drifted.ArtifactKey = "apps/app-1/replaced.ext4"
	if _, err := engine.ResolveAppTaskRuntime(context.Background(), drifted); !errors.Is(err, state.ErrAppTaskDeploymentUnavailable) {
		t.Fatalf("drift error = %v, want ErrAppTaskDeploymentUnavailable", err)
	}
}

func TestAppTaskReservationUsesNodeCapacityWithoutConsumingServingConcurrency(t *testing.T) {
	ledger := NewNodeLedger()
	base := Request{
		AppID: "app-1", DeploymentID: "dep-1", Plan: api.PlanPro,
		RAMMB: 256, VCPU: 1, CPUMillicores: 250, MaxConcurrency: 1,
		NodeID: "node-a", NodeCeilingMB: 4096, VCPUBudget: 8, CPUBudgetMillicores: 8000,
	}
	serving := base
	serving.Instance = "serving-1"
	if err := ledger.Admit(serving); err != nil {
		t.Fatalf("admit serving: %v", err)
	}
	task := base
	task.Instance, task.Kind = "task-1", KindAppTask
	if err := ledger.Admit(task); err != nil {
		t.Fatalf("app task should fit node capacity independently of serving concurrency: %v", err)
	}
	secondServing := base
	secondServing.Instance = "serving-2"
	if err := ledger.Admit(secondServing); err == nil {
		t.Fatal("second serving replica bypassed max concurrency")
	}
}

type unexpectedAppTaskVMM struct{ *recordingRoutedAppTaskVMM }

func (r *unexpectedAppTaskVMM) RestoreAppTask(context.Context, string, AppTaskRestoreSpec) (*AppTaskRestoreOutcome, error) {
	return &AppTaskRestoreOutcome{Instance: "wrong-instance"}, nil
}

// Spec §4.7: a suspended account's apps are parked. A task queued (or
// waiting on a retry) before the suspension must fail without placement.
func TestAppTaskRefusedAfterAccountSuspension(t *testing.T) {
	store, account, app, _, tasks := newAppTaskCoordinatorFixture(t, 1, 30, 2048)
	if err := store.UpdateAccountStatus(context.Background(), account.ID, state.AccountSuspended); err != nil {
		t.Fatal(err)
	}
	engine := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	backend := appTaskBackendFunc(func(ctx context.Context, request AppTaskRestoreRequest) (AppTaskSession, error) {
		resolved, err := engine.ResolveAppTaskRuntime(ctx, request)
		if err != nil {
			return nil, err
		}
		resolved.release()
		return nil, errors.New("suspended account task was placed")
	})
	coordinator := NewAppTaskCoordinator(store, backend, appTaskCoordinatorTestConfig(), nil)
	if processed, err := coordinator.ProcessNext(context.Background()); err != nil || !processed {
		t.Fatalf("ProcessNext = %v, %v", processed, err)
	}
	row, err := store.AppTaskByID(context.Background(), account.ID, app.ID, tasks[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if row.Status != state.AppTaskFailed || row.FailureCode == nil || *row.FailureCode != accountInactiveFailureCode {
		t.Fatalf("task = %s/%v, want failed/%s", row.Status, row.FailureCode, accountInactiveFailureCode)
	}
}

func TestMigrationCredentialsOnlyReachPersistedReleaseTasks(t *testing.T) {
	store, account, app, dep, tasks := newAppTaskCoordinatorFixture(t, 1, 30, 2048)
	ctx := context.Background()
	for _, row := range []state.AppSecret{
		{AccountID: account.ID, AppID: app.ID, Scope: dep.Scope, Key: "DATABASE_URL", Ciphertext: []byte("runtime"), ManagedPostgresBindingID: "rw", ManagedPostgresAccess: "read_write", ManagedCredentialRef: "rw-secret", ManagedCredentialGeneration: 1},
		{AccountID: account.ID, AppID: app.ID, Scope: dep.Scope, Key: "SCHEMA_DSN", Ciphertext: []byte("ddl"), ManagedPostgresBindingID: "ddl", ManagedPostgresAccess: "migration", ManagedCredentialRef: "ddl-secret", ManagedCredentialGeneration: 1},
	} {
		if err := store.PutManagedPostgresSecret(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	engine := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	loaded, err := engine.loadSealedEnvDeliveryFor(ctx, account.ID, app.ID, dep.Scope, nil)
	if err != nil || len(loaded.Entries) != 1 || len(loaded.Candidates) != 1 || loaded.Entries[0].Key != "DATABASE_URL" {
		t.Fatalf("serving delivery = %+v, %v", loaded, err)
	}
	if _, err := engine.loadSealedEnvDeliveryFor(ctx, account.ID, app.ID, dep.Scope, map[string]string{"SCHEMA_DSN": "secret:SCHEMA_DSN"}); err == nil {
		t.Fatal("explicit serving/sidecar reference admitted DDL")
	}
	release, err := store.CreateAppTask(ctx, state.CreateAppTaskParams{AccountID: account.ID, AppID: app.ID, DeploymentID: dep.ID, Kind: state.AppTaskKindRelease, Command: []string{"bin/migrate"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range []state.AppTask{tasks[0], release} {
		request := AppTaskRestoreRequest{ID: task.ID, AccountID: account.ID, AppID: app.ID, DeploymentID: dep.ID, Kind: task.Kind, DeploymentScope: task.DeploymentScope, ArtifactKey: task.ArtifactKey, ImageDigest: task.ImageDigest}
		resolved, err := engine.ResolveAppTaskRuntime(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		want := 1
		if task.Kind == state.AppTaskKindRelease {
			want = 2
		}
		if len(resolved.Spec.App.SealedEnv) != want {
			t.Fatalf("%s task got %d secrets, want %d", task.Kind, len(resolved.Spec.App.SealedEnv), want)
		}
		resolved.release()
		if task.Kind != state.AppTaskKindRelease {
			request.Kind = state.AppTaskKindRelease
			if _, err := engine.ResolveAppTaskRuntime(ctx, request); !errors.Is(err, state.ErrAppTaskDeploymentUnavailable) {
				t.Fatalf("forged release kind = %v", err)
			}
		}
	}
	// A serving allowlist must not accidentally omit the release's DDL binding.
	if _, err := engine.loadSealedEnvDeliveryForTask(ctx, account.ID, app.ID, dep.Scope, map[string]string{"DATABASE_URL": "secret:DATABASE_URL"}, true); err != nil {
		t.Fatal(err)
	}
	// Companion grants use the same policy and cannot elevate their audience.
	dep.Sidecars = json.RawMessage(`[{"name":"worker","image":"r/x@sha256:01","type":"sidecar","env_secrets":{"SCHEMA_DSN":"secret:SCHEMA_DSN"}}]`)
	if _, err := store.SetDeploymentSidecarLayer(ctx, state.DeploymentSidecarLayer{
		DeploymentID: dep.ID, SidecarName: "worker", StorageKey: "apps/app/worker.ext4",
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := engine.sidecarsForDeployment(ctx, dep, account.ID); err == nil || !strings.Contains(err.Error(), "restricted to release tasks") {
		t.Fatalf("sidecar migration grant = %v, want release restriction", err)
	}
}
