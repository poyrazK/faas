// adr: 590
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAPIInvocationDefaultIngressRejectsStagePins(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	ctx := t.Context()
	project, err := (*e.Store()).CreateProject(ctx, state.Project{AccountID: (*e.Acct()).ID, Slug: "api-invoke-stage"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (*e.Store()).CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: (*e.Acct()).ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	app, err := (*e.Store()).CreateApp(ctx, state.App{AccountID: (*e.Acct()).ID, ProjectID: project.ID, Slug: "api-invoke-stage", Type: state.AppTypeApp,
		RAMMB: 256, MaxConcurrency: 2, Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := (*e.Store()).CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "staging", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	if err := (*e.Store()).MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	release, err := (*e.Store()).PublishProjectReleaseSet(ctx, (*e.Acct()).ID, project.ID, "staging", 1800, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: dep.ID}})
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
			response := e.Do(t, http.MethodPost, "/v1/apps/api-invoke-stage/invoke/async", req, requestHeaders)
			if response.Code != http.StatusGone {
				t.Fatalf("%s body=%t default ingress = %d %s", pin.header, bodyPin, response.Code, response.Body.String())
			}
		}
	}
	rows, err := (*e.Store()).ListInvocationsForApp(ctx, app.ID)
	if err != nil || len(rows) != 0 {
		t.Fatalf("stage-pin rejection persisted work: %d %v", len(rows), err)
	}
}
