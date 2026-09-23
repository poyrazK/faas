// adr: 230

package sched

import (
	"context"
	"errors"
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
	return apptaskproto.Result{Status: apptaskproto.StatusSucceeded, ExitCode: &exit, Stdout: []byte("done\n")}, nil
}

func (r *recordingRoutedAppTaskVMM) ExecuteAppTaskWithOutput(ctx context.Context, nodeID, instance string, request apptaskproto.Request, _ apptaskproto.OutputReceiver) (apptaskproto.Result, error) {
	r.streamed = true
	return r.ExecuteAppTask(ctx, nodeID, instance, request)
}

func (r *recordingRoutedAppTaskVMM) Destroy(_ context.Context, nodeID, _ string) error {
	r.destroyNode = nodeID
	return nil
}

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
	if outcome.Status != state.AppTaskSucceeded || outcome.StdoutTail != "done\n" || outcome.ExitCode == nil || *outcome.ExitCode != 0 {
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
