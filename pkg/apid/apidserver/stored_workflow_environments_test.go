package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261104120000 — stored workflow routes take an environment: a
// stage's workflows are authored there, production's list never shows them,
// a workflow is reached only through its own environment, a stage runs only
// manual and schedule workflows (on its own deployment), the routes that test
// or watch a workflow refuse a stage, and stage writes wait for the runtime
// key.
func TestStoredWorkflowsInEnvironment(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	(*e.S()).WithWorkflowRuntimeEnabled(true)
	ctx := context.Background()
	store, account := *e.Store(), *e.Acct()
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "wf-env"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "wf-env-api",
		Type: state.AppTypeApp, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive,
		ImageDigest: "sha256:x", Workflows: json.RawMessage(`[]`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	stageDeployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive,
		ImageDigest: "sha256:y", Scope: "staging", Workflows: json.RawMessage(`[]`)})
	if err != nil {
		t.Fatal(err)
	}
	const base = "/v1/apps/wf-env-api/workflows/stored"
	const stage = "?environment=staging"
	spec := func(name string) api.SaveStoredWorkflowDraftRequest {
		return api.SaveStoredWorkflowDraftRequest{Definition: api.WorkflowSpec{Name: name,
			Trigger: &api.WorkflowTriggerSpec{Type: "schedule", Schedule: "0 * * * *"},
			Steps:   []api.WorkflowStepSpec{{Name: "main", Path: "/run"}}}}
	}

	// A stage's workflow waits for the runtime key.
	if res := e.Do(t, http.MethodPut, base+"/nightly"+stage, spec("nightly"), nil); res.Code != http.StatusNotImplemented {
		t.Fatalf("stage save before the key = %d %s, want 501", res.Code, res.Body)
	}
	(*e.S()).EnableStageStoredWorkflowsForTest(t)

	tenantJSON[api.StoredWorkflowResponse](t, e, http.MethodPut, base+"/report", spec("report"), http.StatusOK)
	draft := tenantJSON[api.StoredWorkflowResponse](t, e, http.MethodPut, base+"/nightly"+stage, spec("nightly"), http.StatusOK)
	published := tenantJSON[api.StoredWorkflowResponse](t, e, http.MethodPost, base+"/nightly/publish"+stage,
		api.PublishStoredWorkflowRequest{ExpectedVersion: draft.Version}, http.StatusOK)
	if !published.Enabled || published.PublishedVersion == 0 {
		t.Fatalf("stage schedule publish = %+v, want published and enabled", published)
	}
	onEvent := api.SaveStoredWorkflowDraftRequest{Definition: api.WorkflowSpec{Name: "on-paid",
		Trigger: &api.WorkflowTriggerSpec{Type: "event", Source: "billing", EventType: "paid"},
		Steps:   []api.WorkflowStepSpec{{Name: "main", Path: "/paid"}}}}
	eventDraft := tenantJSON[api.StoredWorkflowResponse](t, e, http.MethodPut, base+"/on-paid"+stage, onEvent, http.StatusOK)
	eventPublished := tenantJSON[api.StoredWorkflowResponse](t, e, http.MethodPost, base+"/on-paid/publish"+stage,
		api.PublishStoredWorkflowRequest{ExpectedVersion: eventDraft.Version}, http.StatusOK)
	on := true
	if res := e.Do(t, http.MethodPut, base+"/on-paid/enabled"+stage, api.SetStoredWorkflowEnabledRequest{ExpectedVersion: eventPublished.Version, Enabled: &on}, nil); res.Code != http.StatusConflict {
		t.Fatalf("enable stage event workflow = %d %s, want 409", res.Code, res.Body)
	}

	// A manual start in the stage runs the stage's workflow on its deployment;
	// production has no such workflow.
	run := tenantJSON[api.WorkflowRunResponse](t, e, http.MethodPost, "/v1/apps/wf-env-api/workflows/nightly/runs?environment=staging", map[string]any{}, http.StatusCreated)
	if run.DeploymentID != stageDeployment.ID {
		t.Fatalf("stage run deployment = %q, want %s", run.DeploymentID, stageDeployment.ID)
	}
	tenantJSON[api.Problem](t, e, http.MethodPost, "/v1/apps/wf-env-api/workflows/nightly/runs", map[string]any{}, http.StatusNotFound)
	tenantJSON[api.Problem](t, e, http.MethodPost, "/v1/apps/wf-env-api/workflows/on-paid/runs?environment=staging", map[string]any{}, http.StatusNotFound)

	for _, tc := range []struct{ path, want string }{
		{base, "report"},
		{base + "?environment=production", "report"},
		{base + stage, "nightly"},
	} {
		list := tenantJSON[api.ListStoredWorkflowsResponse](t, e, http.MethodGet, tc.path, nil, http.StatusOK)
		if tc.want == "nightly" && len(list.StoredWorkflows) == 2 {
			list.StoredWorkflows = list.StoredWorkflows[:1] // nightly sorts before on-paid
		}
		if len(list.StoredWorkflows) != 1 || list.StoredWorkflows[0].Name != tc.want {
			t.Fatalf("GET %s = %+v, want only %s", tc.path, list.StoredWorkflows, tc.want)
		}
	}
	tenantJSON[api.Problem](t, e, http.MethodGet, base+"?environment=missing", nil, http.StatusNotFound)

	// A workflow is reached only through its own environment.
	tenantJSON[api.Problem](t, e, http.MethodGet, base+"/nightly", nil, http.StatusNotFound)
	tenantJSON[api.Problem](t, e, http.MethodGet, base+"/report"+stage, nil, http.StatusNotFound)
	tenantJSON[api.Problem](t, e, http.MethodPut, base+"/report"+stage, spec("report"), http.StatusConflict)
	tenantJSON[api.StoredWorkflowResponse](t, e, http.MethodGet, base+"/nightly"+stage, nil, http.StatusOK)
	tenantJSON[api.ListStoredWorkflowRevisionsResponse](t, e, http.MethodGet, base+"/nightly/revisions"+stage, nil, http.StatusOK)

	// Routes that test or watch a workflow refuse a stage.
	tenantJSON[api.Problem](t, e, http.MethodGet, base+"/nightly/health"+stage, nil, http.StatusNotImplemented)

	if res := e.Do(t, http.MethodDelete, base+"/nightly?expected_version="+strconv.FormatInt(published.Version, 10)+"&environment=staging", nil, nil); res.Code != http.StatusNoContent {
		t.Fatalf("stage delete = %d %s", res.Code, res.Body)
	}
}
