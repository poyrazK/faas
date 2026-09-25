package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPatchGitHubDeployPolicyPreviewEnvironmentSource(t *testing.T) {
	e, _ := newTestServerWithCapturingNotifier(t, api.PlanPro)
	project, err := e.store.CreateProject(t.Context(), state.Project{AccountID: e.acct.ID, Slug: "preview-policy"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateApp(t.Context(), state.App{
		AccountID: e.acct.ID, ProjectID: project.ID, Slug: "preview-policy-app",
		Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, Status: state.AppActive,
	}); err != nil {
		t.Fatal(err)
	}
	patch := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPatch, "/", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.SetPathValue("slug", "preview-policy-app")
		response := httptest.NewRecorder()
		e.s.patchGitHubDeployPolicy(response, req, e.acct)
		return response
	}

	response := patch(`{"preview_environment_from":"staging"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("set preview environment source status=%d body=%s", response.Code, response.Body.String())
	}
	var stored githubDeployPolicyResponse
	if err := json.Unmarshal(response.Body.Bytes(), &stored); err != nil {
		t.Fatal(err)
	}
	if stored.PreviewEnvironmentFrom != "staging" {
		t.Fatalf("stored preview environment source=%q", stored.PreviewEnvironmentFrom)
	}

	response = patch(`{"preview_environment_from":""}`)
	if response.Code != http.StatusOK {
		t.Fatalf("clear preview environment source status=%d body=%s", response.Code, response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), &stored); err != nil {
		t.Fatal(err)
	}
	if stored.PreviewEnvironmentFrom != "" {
		t.Fatalf("cleared preview environment source=%q", stored.PreviewEnvironmentFrom)
	}

	response = patch(`{"preview_environment_from":"../production"}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid preview environment source status=%d body=%s", response.Code, response.Body.String())
	}
}
