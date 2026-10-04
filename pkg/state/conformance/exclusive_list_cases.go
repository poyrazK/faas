package conformance

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/exclusivework"
	"github.com/onebox-faas/faas/pkg/state"
)

func testExclusiveOperationWorkHistory(t *testing.T, fx *Fixture) {
	owners, ok := fx.Store.(state.ExclusiveWorkStore)
	if !ok {
		t.Fatal("store does not implement ExclusiveWorkStore")
	}
	job, err := fx.Store.JobCreate(
		fx.Ctx, fx.Account.ID, "exclusive-history-"+uuid.NewString()[:8], "batch",
		"ghcr.io/onebox-faas/conformance:latest", []string{"/bin/true"}, 128, 60, 1, 0, nil,
	)
	if err != nil {
		t.Fatalf("JobCreate: %v", err)
	}
	if _, err := owners.UpsertExclusiveWorkPolicy(fx.Ctx, fx.Account.ID, exclusivework.Policy{
		Name: "history", Scope: "account", MemberAppIDs: []string{fx.App.ID}, MemberJobIDs: []string{job.ID},
		Contention: "queue",
	}); err != nil {
		t.Fatalf("UpsertExclusiveWorkPolicy: %v", err)
	}

	appOperation, _, err := owners.AdmitExclusiveOperation(fx.Ctx, state.ExclusiveAdmission{
		AccountID: fx.Account.ID, AppID: fx.App.ID, PolicyName: "history",
		Key: json.RawMessage(`"app-task"`), Request: json.RawMessage(`{"kind":"app_task"}`),
	})
	if err != nil {
		t.Fatalf("AdmitExclusiveOperation(app): %v", err)
	}
	appOwner, err := owners.ClaimExclusiveOperation(fx.Ctx, fx.Account.ID, appOperation.ID, "history/app")
	if err != nil {
		t.Fatalf("ClaimExclusiveOperation(app): %v", err)
	}
	if err := fx.Store.SetDeploymentRootfs(fx.Ctx, fx.Deployment.ID, "/local/exclusive-history.ext4", "apps/conformance/exclusive-history.ext4", 4096); err != nil {
		t.Fatalf("SetDeploymentRootfs: %v", err)
	}
	task, err := fx.Store.CreateAppTask(fx.Ctx, state.CreateAppTaskParams{
		AccountID: fx.Account.ID, AppID: fx.App.ID, DeploymentID: fx.Deployment.ID,
		ExclusiveOperationID: appOperation.ID, ExclusiveGeneration: appOwner.Generation,
		Kind: state.AppTaskKindManual, Command: []string{"bin/check"},
	})
	if err != nil {
		t.Fatalf("CreateAppTask: %v", err)
	}
	appTasks, err := fx.Store.ListAppTasksByExclusiveOperation(fx.Ctx, fx.Account.ID, appOperation.ID)
	if err != nil || len(appTasks) != 1 || appTasks[0].ID != task.ID || appTasks[0].ExclusiveGeneration != appOwner.Generation {
		t.Fatalf("ListAppTasksByExclusiveOperation = %+v, %v; want the admitted generation", appTasks, err)
	}
	appTasks, err = fx.Store.ListAppTasksByExclusiveOperation(fx.Ctx, uuid.NewString(), appOperation.ID)
	if err != nil || len(appTasks) != 0 {
		t.Fatalf("cross-account app-task history = %+v, %v; want empty", appTasks, err)
	}

	jobOperation, _, err := owners.AdmitExclusiveOperation(fx.Ctx, state.ExclusiveAdmission{
		AccountID: fx.Account.ID, JobID: job.ID, PolicyName: "history",
		Key: json.RawMessage(`"job-run"`), Request: json.RawMessage(`{"kind":"job_run"}`),
	})
	if err != nil {
		t.Fatalf("AdmitExclusiveOperation(Job): %v", err)
	}
	jobOwner, err := owners.ClaimExclusiveOperation(fx.Ctx, fx.Account.ID, jobOperation.ID, "history/job")
	if err != nil {
		t.Fatalf("ClaimExclusiveOperation(Job): %v", err)
	}
	run, _, err := fx.Store.JobRunCreate(fx.Ctx, job.ID, fx.Account.ID, "manual", nil, nil, nil, nil, 1,
		state.JobRunOptions{ID: uuid.NewString(), ExclusiveOperationID: jobOperation.ID, ExclusiveGeneration: jobOwner.Generation},
	)
	if err != nil {
		t.Fatalf("JobRunCreate: %v", err)
	}
	runs, err := fx.Store.JobRunListByExclusiveOperation(fx.Ctx, fx.Account.ID, jobOperation.ID)
	if err != nil || len(runs) != 1 || runs[0].ID != run.ID || runs[0].ExclusiveGeneration != jobOwner.Generation {
		t.Fatalf("JobRunListByExclusiveOperation = %+v, %v; want the admitted generation", runs, err)
	}
	runs, err = fx.Store.JobRunListByExclusiveOperation(fx.Ctx, uuid.NewString(), jobOperation.ID)
	if err != nil || len(runs) != 0 {
		t.Fatalf("cross-account JobRun history = %+v, %v; want empty", runs, err)
	}
}
