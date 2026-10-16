// adr: 1351 — exact candidate service allocation, expiry and routing preservation.
package apidserver_test

import (
	"encoding/json"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"testing"
)

func TestCheckedProjectReleaseStagingAPI(t *testing.T) {
	ctx := t.Context()
	e := apidserver.Setup(t, api.PlanPro)
	store, account := *e.Store(), *e.Acct()
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "staged-project"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "staged-api", WorkloadName: "staged-api", Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 5, Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	candidate := ""
	for _, dark := range []bool{false, true} {
		dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + uuid.NewString(), TrafficPercentExplicit: dark})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetDeploymentRootfs(ctx, dep.ID, "/test/rootfs", "staged/"+dep.ID, 4096); err != nil {
			t.Fatal(err)
		}
		if dark {
			err = store.MarkDeploymentLiveDark(ctx, dep.ID)
			candidate = uuid.MustParse(dep.ID).String()
		} else {
			err = store.MarkDeploymentLive(ctx, dep.ID)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	appManifest := app.Manifest
	appManifest.ExecutionMode = api.ExecutionModeService
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &appManifest}); err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	if _, err := store.SetBindingReleasePolicy(ctx, account.ID, app.ID, "production", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero}); err != nil {
		t.Fatal(err)
	}
	base := "/v1/projects/staged-project/environments/production/release-sets"
	prepared := e.Do(t, http.MethodPost, base+"/prepare", api.PublishProjectReleaseSetRequest{TTLSeconds: 1800, Deployments: map[string]string{app.Slug: candidate}}, nil)
	if prepared.Code != http.StatusOK {
		t.Fatalf("prepare: %d %s", prepared.Code, prepared.Body.String())
	}
	var manifest api.CheckedProjectReleaseManifest
	if err := json.Unmarshal(prepared.Body.Bytes(), &manifest); err != nil {
		t.Fatal(err)
	}
	request := api.StageProjectReleaseRequest{Manifest: manifest, TTLSeconds: 1800}
	staged := e.Do(t, http.MethodPost, base+"/staging", request, map[string]string{"Idempotency-Key": "stage-checked"})
	if staged.Code != http.StatusOK {
		t.Fatalf("stage: %d %s", staged.Code, staged.Body.String())
	}
	var stage api.ProjectReleaseStage
	if err := json.Unmarshal(staged.Body.Bytes(), &stage); err != nil {
		t.Fatal(err)
	}
	if stage.Status != "waiting" || len(stage.Members) != 1 || stage.Members[0].DeploymentID != candidate {
		t.Fatalf("stage: %+v", stage)
	}
	replay := e.Do(t, http.MethodPost, base+"/staging", request, map[string]string{"Idempotency-Key": "stage-checked"})
	if replay.Code != http.StatusOK || replay.Body.String() != staged.Body.String() {
		t.Fatalf("idempotent staging: %d %s", replay.Code, replay.Body.String())
	}
	status := e.Do(t, http.MethodGet, base+"/staging/"+stage.ID, nil, nil)
	if status.Code != http.StatusOK {
		t.Fatalf("status: %d %s", status.Code, status.Body.String())
	}
	invalid := request
	invalid.TTLSeconds = 0
	rejected := e.Do(t, http.MethodPost, base+"/staging", invalid, nil)
	if rejected.Code != http.StatusBadRequest {
		t.Fatalf("invalid TTL accepted: %d", rejected.Code)
	}
	cancelled := e.Do(t, http.MethodDelete, base+"/staging/"+stage.ID, nil, nil)
	if cancelled.Code != http.StatusOK {
		t.Fatalf("cancel: %d %s", cancelled.Code, cancelled.Body.String())
	}
	var out api.ProjectReleaseStage
	_ = json.Unmarshal(cancelled.Body.Bytes(), &out)
	if out.Status != "cancelled" {
		t.Fatalf("cancel: %+v", out)
	}
	active := e.Do(t, http.MethodGet, base+"/active", nil, nil)
	if active.Code != http.StatusNotFound {
		t.Fatalf("staging published graph: %d %s", active.Code, active.Body.String())
	}
}
