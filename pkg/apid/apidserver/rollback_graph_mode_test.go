package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRollbackGraphRequiresProjectRecovery(t *testing.T) {
	t.Setenv("FAAS_API_CONTRACT_DIFF_ENABLED", "0")
	e, _, _, _ := apidserver.CheckedRollbackAPIFixture(t)
	store := *e.Store()
	account := *e.Acct()
	project, err := store.CreateProject(t.Context(), state.Project{AccountID: account.ID, Slug: "rollback-graph-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, ProjectID: project.ID, WorkloadName: "api", Slug: "graph-app-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 5, Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	target := createRollbackContractDeployment(t, store, app, "production", false)
	current := createRollbackContractDeployment(t, store, app, "production", false)
	if _, err = store.PublishProjectReleaseSet(t.Context(), account.ID, project.ID, "production", 1800, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: current.ID}}); err != nil {
		t.Fatal(err)
	}
	for _, checked := range []bool{false, true} {
		req := api.RollbackRequest{TargetDeploymentID: &target.ID}
		if checked {
			req.ExpectedCurrentDeploymentID = &current.ID
		}
		response := e.Do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/rollback", req, nil)
		var problem api.Problem
		if response.Code != http.StatusConflict || json.Unmarshal(response.Body.Bytes(), &problem) != nil || problem.Code != api.CodeProjectReleaseRollbackRequired {
			t.Fatalf("checked=%v: %d %s", checked, response.Code, response.Body)
		}
	}
	actual, err := state.ResolveProductionDeployment(t.Context(), store, app.ID)
	if err != nil || actual.ID != current.ID {
		t.Fatalf("rejected request changed production: %+v %v", actual, err)
	}
}
func TestCheckedRollbackExecutionModeChangeIsTerminal(t *testing.T) {
	t.Setenv("FAAS_API_CONTRACT_DIFF_ENABLED", "0")
	e, app, target, current := apidserver.CheckedRollbackAPIFixture(t)
	store := *e.Store()
	account := *e.Acct()
	response := e.Do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/rollback", api.RollbackRequest{TargetDeploymentID: &target.ID, ExpectedCurrentDeploymentID: &current.ID}, nil)
	var accepted api.DeploymentResponse
	if response.Code != http.StatusAccepted || json.Unmarshal(response.Body.Bytes(), &accepted) != nil || accepted.RollbackOperation == nil {
		t.Fatalf("admit: %d %s", response.Code, response.Body)
	}
	if err := store.MarkDeploymentLive(t.Context(), target.ID); err != nil {
		t.Fatal(err)
	}
	manifest := app.Manifest
	manifest.ExecutionMode = api.ExecutionModeService
	manifest.ServiceReplicas = &state.ServiceReplicas{Min: 1, Max: 2, Desired: 1}
	if _, err := store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	if err := (*e.S()).CheckedRollbackSweep(t.Context()); err != nil {
		t.Fatal(err)
	}
	operation, err := store.GetCheckedRollback(t.Context(), account.ID, app.ID, accepted.RollbackOperation.ID)
	if err != nil || operation.Status != "failed" || operation.Code != "rollback_execution_mode_changed" {
		t.Fatalf("mode fence: %+v %v", operation, err)
	}
	serving, err := store.DeploymentByID(t.Context(), current.ID)
	if err != nil || serving.TrafficPercent != 100 {
		t.Fatalf("mode failure moved current traffic: %+v %v", serving, err)
	}
}
