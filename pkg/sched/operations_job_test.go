// adr: 664
package sched

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEngineJobOperationDispatchSurvivesRestartAndTemplateEdit(t *testing.T) {
	store := state.NewMemStore()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, "job-operation@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.JobCreate(ctx, account.ID, "export-job", "batch", "registry.example/export:v1", []string{"node", "export.mjs"}, 128, 60, 1, 3, []byte(`{"VERSION":"original"}`))
	if err != nil {
		t.Fatal(err)
	}
	job, err = store.JobSetImageMaterialization(ctx, job.ID, job.ImageRef, "ready", "sha256:"+strings.Repeat("a", 64), "jobs/"+job.ID+".ext4", "")
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "job-operation", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:app"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.CreatePlatformTenant(ctx, account.ID, "alice", "Alice", 100)
	if err != nil {
		t.Fatal(err)
	}
	def, err := store.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: account.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: app.ID, Scope: dep.Scope, DeploymentID: dep.ID, Spec: api.OperationDefinitionSpec{Name: "export", Job: job.Name, Method: "POST", Path: "/exports", Owner: api.OperationOwnerPlatformTenant, ProgressStages: []string{"generating"}, InputSchema: []byte(`{"type":"object"}`), OutputSchema: []byte(`{"type":"object","required":["file"],"properties":{"file":{"type":"string"}}}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	op, fresh, err := store.AdmitOperation(ctx, state.OperationAdmission{AccountID: account.ID, PlatformTenantID: tenant.ID, DefinitionID: def.ID, IdempotencyKey: "export", Input: []byte(`{"count":1}`)})
	if err != nil || !fresh {
		t.Fatal(err)
	}
	image := "registry.example/replacement:v2"
	timeout, retries := 120, 10
	if _, err := store.JobUpdate(ctx, job.ID, []string{"replacement"}, &image, nil, &timeout, nil, &retries, []byte(`{"VERSION":"replacement"}`), nil); err != nil {
		t.Fatal(err)
	}
	vmm := &recordingJobVMM{}
	engine := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0").WithJobLeaser(AdaptJobLeaser(NewMemLeaser(nil))).WithJobVmmClient(vmm)
	wake, err := engine.WakeJob(ctx, account.ID, op.JobRunID, 0)
	if err != nil {
		t.Fatal(err)
	}
	spec := vmm.spec
	if spec.ImageRef != job.ImageStorageKey || spec.Command[0] != job.Command[0] || spec.TaskTimeoutSec != job.TaskTimeoutS || spec.Env["VERSION"] != "original" || spec.Env["GREGALE_CUSTOMER_OPERATION_INPUT"] != `{"count":1}` {
		t.Fatalf("lost frozen snapshot: %+v", spec)
	}
	capability := spec.Env["GREGALE_CUSTOMER_OPERATION_JOB_CAPABILITY"]
	if capability == "" || capability == spec.LeaseToken || spec.Env["GREGALE_CUSTOMER_OPERATION_JOB_INSTANCE_ID"] != wake.InstanceID {
		t.Fatal("missing task-bound capability")
	}
	restarted := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0").WithJobLeaser(AdaptJobLeaser(NewMemLeaser(nil))).WithJobVmmClient(vmm)
	if _, err := restarted.WakeJob(ctx, account.ID, op.JobRunID, 0); !errors.Is(err, ErrJobTaskAlreadyClaimed) {
		t.Fatal("restart dispatched twice", err)
	}
	if vmm.calls != 1 {
		t.Fatal("duplicate boot")
	}
	proof := state.JobOperationAuthority{RunID: op.JobRunID, InstanceID: wake.InstanceID, Generation: 1, Attempt: 1, Capability: capability}
	if _, err := store.ReportOperationJob(ctx, op.ID, proof, "result", api.OperationJobReportRequest{ReportID: "result", Result: json.RawMessage(`{"file":"export.csv"}`)}); err != nil {
		t.Fatal(err)
	}
	if err := engine.HandleJobExit(ctx, account.ID, op.JobRunID, 0, 0, "succeeded", string(wake.LeaseToken)); err != nil {
		t.Fatal(err)
	}
	got, err := store.OperationByID(ctx, account.ID, tenant.ID, op.ID)
	if err != nil || got.State != api.OperationSucceeded {
		t.Fatalf("native exit=%s %v", got.State, err)
	}
}
func TestEngineOrdinaryJobStripsOperationContext(t *testing.T) {
	store := state.NewMemStore()
	account, _, run := seedJobRun(t, store, []byte(`{"GREGALE_CUSTOMER_OPERATION_ID":"spoofed","GREGALE_CUSTOMER_OPERATION_JOB_CAPABILITY":"spoofed"}`), nil)
	vmm := &recordingJobVMM{}
	engine := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0").WithJobLeaser(AdaptJobLeaser(NewMemLeaser(nil))).WithJobVmmClient(vmm)
	if _, err := engine.WakeJob(context.Background(), account.ID, run.ID, 0); err != nil {
		t.Fatal(err)
	}
	for key := range vmm.spec.Env {
		if strings.HasPrefix(key, "GREGALE_CUSTOMER_OPERATION_") {
			t.Fatal("ordinary job spoofed operation context")
		}
	}
}
