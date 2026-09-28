package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestScenarioTestRegistrationRequiresSameRunAndCleanup(t *testing.T) {
	e := setup(t, api.PlanPro)
	const runID = "0123456789abcdef0123456789abcdef"
	create := func(project string, workspaceID string) api.DevSessionResponse {
		t.Helper()
		rec := e.do(t, "PUT", "/v1/dev/sessions/"+project, api.UpsertDevSessionRequest{WorkspaceID: workspaceID}, nil)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %s: %d %s", project, rec.Code, rec.Body.String())
		}
		var session api.DevSessionResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &session); err != nil {
			t.Fatal(err)
		}
		return session
	}
	apiSession := create("export-api", runID)
	worker := create("export-api-worker", runID)
	otherRun := create("other-run", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	path := "/v1/dev/test-runs/" + runID
	bad := e.do(t, "PUT", path, api.RegisterScenarioTestRequest{Members: []api.ScenarioTestWorkload{
		{Workload: "api", AppSlug: apiSession.App.Slug}, {Workload: "worker", AppSlug: otherRun.App.Slug},
	}}, nil)
	if bad.Code != http.StatusNotFound {
		t.Fatalf("cross-run registration: %d %s", bad.Code, bad.Body.String())
	}
	good := e.do(t, "PUT", path, api.RegisterScenarioTestRequest{Members: []api.ScenarioTestWorkload{
		{Workload: "api", AppSlug: apiSession.App.Slug}, {Workload: "worker", AppSlug: worker.App.Slug},
	}}, nil)
	if good.Code != http.StatusNoContent {
		t.Fatalf("register: %d %s", good.Code, good.Body.String())
	}
	if rec := e.do(t, "DELETE", path, nil, nil); rec.Code != http.StatusConflict {
		t.Fatalf("active deletion status = %d", rec.Code)
	}
	for _, project := range []string{"export-api-worker", "export-api"} {
		if rec := e.do(t, "DELETE", "/v1/dev/sessions/"+project+"?workspace_id="+runID, nil, nil); rec.Code != http.StatusNoContent {
			t.Fatalf("destroy %s: %d %s", project, rec.Code, rec.Body.String())
		}
	}
	if rec := e.do(t, "DELETE", path, nil, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("cleanup: %d %s", rec.Code, rec.Body.String())
	}
}
