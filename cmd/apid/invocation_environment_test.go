// adr: 566
package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAPIInvocationDefaultIngressRejectsStagePins(t *testing.T) {
	e := setup(t, api.PlanPro)
	ctx := t.Context()
	project, err := e.store.CreateProject(ctx, state.Project{AccountID: e.acct.ID, Slug: "api-invoke-stage"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: e.acct.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	app, err := e.store.CreateApp(ctx, state.App{AccountID: e.acct.ID, ProjectID: project.ID, Slug: "api-invoke-stage", Type: state.AppTypeApp,
		RAMMB: 256, MaxConcurrency: 2, Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := e.store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "staging", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	release, err := e.store.PublishProjectReleaseSet(ctx, e.acct.ID, project.ID, "staging", 1800, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: dep.ID}})
	if err != nil {
		t.Fatal(err)
	}
	for _, pin := range []struct{ header, id string }{{api.ReleaseHeader, release.ID}, {api.RevisionHeader, dep.ID}} {
		for _, bodyPin := range []bool{false, true} {
			req := api.InvokeRequest{Payload: json.RawMessage(`{}`)}
			requestHeaders := map[string]string{pin.header: pin.id}
			if bodyPin {
				req.Headers, _ = json.Marshal(requestHeaders)
				requestHeaders = nil
			}
			response := e.do(t, http.MethodPost, "/v1/apps/api-invoke-stage/invoke/async", req, requestHeaders)
			if response.Code != http.StatusGone {
				t.Fatalf("%s body=%t default ingress = %d %s", pin.header, bodyPin, response.Code, response.Body.String())
			}
		}
	}
	rows, err := e.store.ListInvocationsForApp(ctx, app.ID)
	if err != nil || len(rows) != 0 {
		t.Fatalf("stage-pin rejection persisted work: %d %v", len(rows), err)
	}
}
