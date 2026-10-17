package apidserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCheckedProjectReleaseQualifiesExactServiceGraph(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	apidserver.EnableAppTaskAPIForTest(&e)
	store, account := *e.Store(), *e.Acct()
	(*e.S()).ConfigureCheckedReleaseCatalog(t, account.ID)
	ctx := context.Background()
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "checked-project"})
	if err != nil {
		t.Fatal(err)
	}
	deployments := map[string]string{}
	var caller state.App
	for _, name := range []string{"checked-api", "checked-billing"} {
		manifest := state.AppManifest{RevisionPinTTLSeconds: 3600}
		if name == "checked-api" {
			manifest.ServiceBindings = []api.AppServiceBinding{{Service: "checked-billing", Binding: "BILLING"}}
		}
		app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: name, WorkloadName: name, Type: state.AppTypeApp, Manifest: manifest})
		if err != nil {
			t.Fatal(err)
		}
		if name == "checked-api" {
			caller = app
		}
		deployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Scope: "production", ImageDigest: "sha256:" + name})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetDeploymentRootfs(ctx, deployment.ID, "/test/rootfs", "test/"+name, 4096); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkAppRuntimeConfigChanged(ctx, app.ID); err != nil {
			t.Fatal(err)
		}
		zero := int64(0)
		if _, err := store.SetBindingReleasePolicy(ctx, account.ID, app.ID, "production", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero}); err != nil {
			t.Fatal(err)
		}
		deployments[name] = deployment.ID
	}
	base := "/v1/projects/" + project.Slug + "/environments/production/release-sets"
	prepared := e.Do(t, http.MethodPost, base+"/prepare", api.PublishProjectReleaseSetRequest{TTLSeconds: 1800, Deployments: deployments}, nil)
	if prepared.Code != 200 {
		t.Fatalf("prepare=%d %s", prepared.Code, prepared.Body.String())
	}
	var manifest api.CheckedProjectReleaseManifest
	if err := json.Unmarshal(prepared.Body.Bytes(), &manifest); err != nil {
		t.Fatal(err)
	}
	deployments = manifest.Deployments
	if manifest.ExpectedActiveReleaseID == nil || *manifest.ExpectedActiveReleaseID != "" || manifest.ServiceTargets[caller.Slug]["checked-billing"] != deployments["checked-billing"] {
		t.Fatalf("manifest=%+v", manifest)
	}
	if _, err := store.ActiveProjectReleaseSet(ctx, account.ID, project.ID, "production"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("prepare routed graph: %v", err)
	}
	blocked := e.Do(t, http.MethodPost, base+"/activate", manifest, nil)
	if blocked.Code != 409 {
		t.Fatalf("unqualified activation=%d %s", blocked.Code, blocked.Body.String())
	}
	target := deployments["checked-billing"]
	for _, command := range [][]string{{api.AppTaskServiceBindingTargetProbeCommand, "undeclared", target}, {api.AppTaskServiceBindingTargetProbeCommand, "checked-billing", deployments[caller.Slug]}} {
		response := e.Do(t, http.MethodPost, "/v1/apps/"+caller.Slug+"/tasks", api.CreateAppTaskRequest{VerificationDeploymentID: deployments[caller.Slug], Command: command}, nil)
		if response.Code != 400 {
			t.Fatalf("invalid service target admission=%d %s", response.Code, response.Body.String())
		}
	}
	taskResponse := e.Do(t, http.MethodPost, "/v1/apps/"+caller.Slug+"/tasks", api.CreateAppTaskRequest{VerificationDeploymentID: deployments[caller.Slug], Command: []string{api.AppTaskServiceBindingTargetProbeCommand, "checked-billing", target}}, nil)
	if taskResponse.Code != http.StatusAccepted {
		t.Fatalf("probe=%d %s", taskResponse.Code, taskResponse.Body.String())
	}
	var admitted api.AppTaskResponse
	if err := json.Unmarshal(taskResponse.Body.Bytes(), &admitted); err != nil {
		t.Fatal(err)
	}
	task, err := store.ClaimNextAppTask(ctx, "graph-test", time.Now().UTC(), time.Minute)
	if err != nil || task.ID != admitted.ID {
		t.Fatalf("claim=%+v %v", task, err)
	}
	task, err = store.MarkAppTaskRunning(ctx, task.ID, *task.LeaseToken, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	report := api.ServiceBindingProbeReport{Service: "checked-billing", TargetDeploymentID: target, DNS: api.ServiceBindingProbeCheck{Status: "passed"}, TLS: api.ServiceBindingProbeCheck{Status: "passed"}, Authorization: api.ServiceBindingProbeCheck{Status: "passed"}, Routing: api.ServiceBindingProbeCheck{Status: "passed"}}
	raw, _ := json.Marshal(report)
	zero := 0
	if _, err := store.CompleteAppTask(ctx, state.CompleteAppTaskParams{ID: task.ID, LeaseToken: *task.LeaseToken, Status: state.AppTaskSucceeded, ExitCode: &zero, StdoutTail: string(raw), FinishedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	ordinary := e.Do(t, http.MethodGet, "/v1/apps/"+caller.Slug+"/bindings?deployment_id="+deployments[caller.Slug], nil, nil)
	var inventory api.AppBindingInventory
	_ = json.Unmarshal(ordinary.Body.Bytes(), &inventory)
	for _, item := range inventory.Bindings {
		if item.Type == api.BindingTypeService && item.Verification != nil {
			t.Fatal("graph probe leaked into ordinary evidence")
		}
	}
	selection, _ := json.Marshal(manifest.ServiceTargets[caller.Slug])
	query := url.Values{"deployment_id": []string{deployments[caller.Slug]}, "service_targets": []string{string(selection)}}
	selected := e.Do(t, http.MethodGet, "/v1/apps/"+caller.Slug+"/bindings?"+query.Encode(), nil, nil)
	if selected.Code != 200 {
		t.Fatalf("selected inventory=%d %s", selected.Code, selected.Body.String())
	}
	activated := e.Do(t, http.MethodPost, base+"/activate", manifest, nil)
	if activated.Code != 201 {
		t.Fatalf("activation=%d %s", activated.Code, activated.Body.String())
	}
	var result api.CheckedProjectReleaseResponse
	if err := json.Unmarshal(activated.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Release.Active || len(result.BindingsChecks) != 2 {
		t.Fatalf("result=%+v", result)
	}
	stale := e.Do(t, http.MethodPost, base+"/activate", manifest, nil)
	if stale.Code != 409 {
		t.Fatalf("stale graph CAS=%d %s", stale.Code, stale.Body.String())
	}

	originalID := result.Release.ID
	preparedAgain := e.Do(t, http.MethodPost, base+"/prepare", api.PublishProjectReleaseSetRequest{TTLSeconds: 1800, Deployments: deployments}, nil)
	if preparedAgain.Code != 200 {
		t.Fatalf("prepare next=%d %s", preparedAgain.Code, preparedAgain.Body.String())
	}
	var next api.CheckedProjectReleaseManifest
	if err := json.Unmarshal(preparedAgain.Body.Bytes(), &next); err != nil {
		t.Fatal(err)
	}
	activatedAgain := e.Do(t, http.MethodPost, base+"/activate", next, nil)
	if activatedAgain.Code != 201 {
		t.Fatalf("next activation=%d %s", activatedAgain.Code, activatedAgain.Body.String())
	}
	var second api.CheckedProjectReleaseResponse
	_ = json.Unmarshal(activatedAgain.Body.Bytes(), &second)
	for _, id := range []string{second.Release.ID, "00000000-0000-4000-8000-000000000001"} {
		rejected := e.Do(t, http.MethodPost, base+"/prepare-rollback", api.PrepareProjectRollbackRequest{ReleaseID: id, TTLSeconds: 1800}, nil)
		if rejected.Code < 400 {
			t.Fatal("active or absent rollback history accepted")
		}
	}
	rollbackPrepared := e.Do(t, http.MethodPost, base+"/prepare-rollback", api.PrepareProjectRollbackRequest{ReleaseID: originalID, TTLSeconds: 1800}, nil)
	if rollbackPrepared.Code != 200 {
		t.Fatalf("prepare rollback=%d %s", rollbackPrepared.Code, rollbackPrepared.Body.String())
	}
	var recovery api.CheckedProjectReleaseManifest
	if err := json.Unmarshal(rollbackPrepared.Body.Bytes(), &recovery); err != nil {
		t.Fatal(err)
	}
	if recovery.RollbackReleaseID != originalID || *recovery.ExpectedActiveReleaseID != second.Release.ID {
		t.Fatalf("recovery selection=%+v", recovery)
	}
	rejected := e.Do(t, http.MethodPost, base+"/activate", recovery, nil)
	if rejected.Code != 400 {
		t.Fatal("forward activation accepted recovery intent")
	}
	rolledBack := e.Do(t, http.MethodPost, base+"/rollback", recovery, map[string]string{"Idempotency-Key": "checked-project-recovery"})
	if rolledBack.Code != 201 {
		t.Fatalf("rollback=%d %s", rolledBack.Code, rolledBack.Body.String())
	}
	var receipt api.CheckedProjectReleaseResponse
	_ = json.Unmarshal(rolledBack.Body.Bytes(), &receipt)
	if receipt.Release.RollbackSourceReleaseID != originalID || receipt.Release.RollbackPreviousReleaseID != second.Release.ID {
		t.Fatalf("receipt=%+v", receipt)
	}
	persisted, err := store.ProjectReleaseSetByID(ctx, account.ID, project.ID, "production", receipt.Release.ID)
	if err != nil || persisted.RollbackSourceReleaseID != originalID {
		t.Fatalf("durable recovery=%+v %v", persisted, err)
	}

	replay := e.Do(t, http.MethodPost, base+"/rollback", recovery, map[string]string{"Idempotency-Key": "checked-project-recovery"})
	if replay.Code != rolledBack.Code || replay.Body.String() != rolledBack.Body.String() {
		t.Fatalf("recovery replay=%d %s", replay.Code, replay.Body.String())
	}
	staleRecovery := e.Do(t, http.MethodPost, base+"/rollback", recovery, nil)
	if staleRecovery.Code != 409 {
		t.Fatalf("stale rollback=%d %s", staleRecovery.Code, staleRecovery.Body.String())
	}

	deactivationPrepared := e.Do(t, http.MethodPost, base+"/prepare-deactivation", nil, nil)
	if deactivationPrepared.Code != 200 {
		t.Fatalf("prepare deactivation=%d %s", deactivationPrepared.Code, deactivationPrepared.Body.String())
	}
	var removal api.CheckedProjectReleaseManifest
	if err := json.Unmarshal(deactivationPrepared.Body.Bytes(), &removal); err != nil {
		t.Fatal(err)
	}
	if !removal.Deactivate || removal.TTLSeconds != 0 || *removal.ExpectedActiveReleaseID != receipt.Release.ID || removal.ServiceTargets[caller.Slug]["checked-billing"] != target {
		t.Fatalf("removal selection=%+v", removal)
	}
	rejectedRemoval := e.Do(t, http.MethodPost, base+"/activate", removal, nil)
	if rejectedRemoval.Code != 400 {
		t.Fatal("activation accepted removal intent")
	}
	removed := e.Do(t, http.MethodPost, base+"/deactivate", removal, map[string]string{"Idempotency-Key": "checked-project-deactivate"})
	if removed.Code != 200 {
		t.Fatalf("deactivation=%d %s", removed.Code, removed.Body.String())
	}
	var removalReceipt api.CheckedProjectReleaseResponse
	if err := json.Unmarshal(removed.Body.Bytes(), &removalReceipt); err != nil {
		t.Fatal(err)
	}
	if removalReceipt.Release.Active || removalReceipt.Release.Deactivation == nil || removalReceipt.Release.Deactivation.ReleaseID != receipt.Release.ID || len(removalReceipt.Release.Deactivation.FallbackMembers) != 2 {
		t.Fatalf("removal receipt=%+v", removalReceipt)
	}
	replayRemoval := e.Do(t, http.MethodPost, base+"/deactivate", removal, map[string]string{"Idempotency-Key": "checked-project-deactivate"})
	if replayRemoval.Code != removed.Code || replayRemoval.Body.String() != removed.Body.String() {
		t.Fatal("deactivation did not replay completed receipt")
	}
	staleRemoval := e.Do(t, http.MethodPost, base+"/deactivate", removal, nil)
	if staleRemoval.Code != 409 {
		t.Fatalf("stale deactivation=%d %s", staleRemoval.Code, staleRemoval.Body.String())
	}
	if _, err := store.ActiveProjectReleaseSet(ctx, account.ID, project.ID, "production"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("deactivation left active graph: %v", err)
	}
	savedRemoval, err := store.ProjectReleaseSetByID(ctx, account.ID, project.ID, "production", receipt.Release.ID)
	if err != nil || savedRemoval.Deactivation == nil || savedRemoval.Deactivation.ID != removalReceipt.Release.Deactivation.ID {
		t.Fatalf("missing durable deactivation: %+v %v", savedRemoval, err)
	}
	generic := e.Do(t, http.MethodPost, base, api.PublishProjectReleaseSetRequest{TTLSeconds: 1800, Deployments: deployments}, nil)
	if generic.Code < 400 {
		t.Fatal("generic publication bypassed enforced graph policy")
	}
}
