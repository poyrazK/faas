// adr: 644
package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func failDiagnosticAPIRun(t *testing.T, e testEnv, id string) {
	t.Helper()
	if err := e.store.CreateWorkflowSteps(t.Context(), id, []*state.WorkflowStep{{StepName: "main"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.ClaimNextDueWorkflowRun(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.StartWorkflowStep(t.Context(), id, "main", 1, json.RawMessage(`{"private":"secret-input"}`)); err != nil {
		t.Fatal(err)
	}
	message, code := "secret-error", 503
	if err := e.store.MarkWorkflowStepAttemptStatus(t.Context(), id, "main", state.WorkflowStepStatusDead, 1, &code, nil, &message); err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkWorkflowRunStatus(t.Context(), id, state.WorkflowRunStatusDead, nil, &message); err != nil {
		t.Fatal(err)
	}
}

func TestWorkflowDiagnosticsAPIReadScopePrivacyRuntimeAndOwnership(t *testing.T) {
	e := setup(t, api.PlanHobby)
	app := seedWorkflowApp(t, e, "diagnose-api")
	created := e.do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/workflows/w1/runs", map[string]any{"private": "secret-input"}, nil)
	var run api.WorkflowRunResponse
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &run) != nil {
		t.Fatalf("create=%d %s", created.Code, created.Body)
	}
	failDiagnosticAPIRun(t, e, run.ID)
	token, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateAPIKey(t.Context(), e.acct.ID, hash, "read diagnostics", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{"Authorization": "Bearer " + token}
	path := "/v1/workflows/runs/" + run.ID + "/diagnostics"
	response := e.do(t, http.MethodGet, path, nil, headers)
	var diagnostics api.WorkflowRunDiagnosticsResponse
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || json.Unmarshal(response.Body.Bytes(), &diagnostics) != nil || !diagnostics.Resume.Eligible || diagnostics.DeploymentID != run.DeploymentID || len(diagnostics.Resume.ReopenedSteps) != 1 || diagnostics.Resume.ReopenedSteps[0] != "main" {
		t.Fatalf("diagnose=%d %s", response.Code, response.Body)
	}
	for _, secret := range []string{"secret-input", "secret-error", `"input"`, `"output"`, `"platform_tenant_id"`} {
		if strings.Contains(response.Body.String(), secret) {
			t.Fatalf("private diagnostic data: %s", response.Body)
		}
	}
	unchanged, _ := e.store.GetWorkflowRun(t.Context(), run.ID)
	history, _ := e.store.ListWorkflowResumes(t.Context(), run.ID)
	if unchanged.Status != state.WorkflowRunStatusDead || unchanged.ResumeCount != 0 || len(history) != 0 {
		t.Fatal("GET changed workflow state")
	}
	write := e.do(t, http.MethodPost, "/v1/workflows/runs/"+run.ID+"/resume", map[string]any{"expected_resume_count": 0}, headers)
	if write.Code != http.StatusForbidden {
		t.Fatalf("read key resumed run: %d %s", write.Code, write.Body)
	}
	e.s.workflowRuntimeEnabled = false
	response = e.do(t, http.MethodGet, path, nil, headers)
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &diagnostics) != nil || diagnostics.Resume.Eligible || diagnostics.Resume.Blockers[0].Code != "runtime_disabled" {
		t.Fatalf("runtime blocker=%d %s", response.Code, response.Body)
	}
	other, err := e.store.CreateAccount(t.Context(), "foreign-diagnostics@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	foreign, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateAPIKey(t.Context(), other.ID, hash, "foreign", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	assertProblem(t, e.do(t, http.MethodGet, path, nil, map[string]string{"Authorization": "Bearer " + foreign}), http.StatusNotFound, api.CodeWorkflowRunNotFound)
}

type unavailableWorkflowDiagnosticsStore struct{ state.Store }

func (s unavailableWorkflowDiagnosticsStore) GetWorkflowRunDiagnostics(context.Context, state.WorkflowDiagnosticsOptions) (api.WorkflowRunDiagnosticsResponse, error) {
	return api.WorkflowRunDiagnosticsResponse{}, errors.New("private database detail")
}

func TestWorkflowDiagnosticsUnavailableFailsClosed(t *testing.T) {
	e := setup(t, api.PlanHobby)
	app := seedWorkflowApp(t, e, "diagnostic-unavailable")
	created := e.do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/workflows/w1/runs", nil, nil)
	var run api.WorkflowRunResponse
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &run) != nil {
		t.Fatalf("create=%d %s", created.Code, created.Body)
	}
	e.s.store = unavailableWorkflowDiagnosticsStore{Store: e.store}
	response := e.do(t, http.MethodGet, "/v1/workflows/runs/"+run.ID+"/diagnostics", nil, nil)
	if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), "private database detail") || strings.Contains(response.Body.String(), `"eligible":true`) {
		t.Fatalf("unavailable snapshot=%d %s", response.Code, response.Body)
	}
}

func TestPlatformTenantSelfWorkflowDiagnostics(t *testing.T) {
	e := setup(t, api.PlanHobby)
	app := seedWorkflowApp(t, e, "tenant-diagnostics")
	required := true
	if _, err := e.store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{PlatformTenantRequired: &required, SetPlatformTenantRequired: true}); err != nil {
		t.Fatal(err)
	}
	tenant, _, err := e.store.CreatePlatformTenant(t.Context(), e.acct.ID, "diagnostic-tenant", "Diagnostic tenant", 10)
	if err != nil {
		t.Fatal(err)
	}
	seedPlatformTenantConsumer(t, e, app.Slug, tenant.ID)
	issue := func(id string) string {
		response := e.do(t, http.MethodPost, "/v1/account/platform-tenants/"+id+"/access-tokens", api.CreatePlatformTenantAccessTokenRequest{Name: "diagnostics", Scopes: []string{api.ScopePlatformTenantInvocationsRead, api.ScopePlatformTenantInvocationsManage}}, nil)
		var token api.CreatePlatformTenantAccessTokenResponse
		if response.Code != http.StatusCreated || json.Unmarshal(response.Body.Bytes(), &token) != nil {
			t.Fatalf("token=%d %s", response.Code, response.Body)
		}
		return token.Token
	}
	token := issue(tenant.ID)
	headers := map[string]string{"Authorization": "Bearer " + token}
	response := e.do(t, http.MethodPost, "/v1/platform-tenant-self/apps/"+app.Slug+"/workflows/w1/runs", nil, headers)
	var run api.WorkflowRunResponse
	if response.Code != http.StatusCreated || json.Unmarshal(response.Body.Bytes(), &run) != nil {
		t.Fatalf("create=%d %s", response.Code, response.Body)
	}
	failDiagnosticAPIRun(t, e, run.ID)
	path := "/v1/platform-tenant-self/workflows/runs/" + run.ID + "/diagnostics"
	response = e.do(t, http.MethodGet, path, nil, headers)
	var diagnostics api.WorkflowRunDiagnosticsResponse
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || json.Unmarshal(response.Body.Bytes(), &diagnostics) != nil || !diagnostics.Resume.Eligible || strings.Contains(response.Body.String(), tenant.ID) {
		t.Fatalf("tenant diagnose=%d %s", response.Code, response.Body)
	}
	other, _, err := e.store.CreatePlatformTenant(t.Context(), e.acct.ID, "foreign-diagnostic-tenant", "Other", 10)
	if err != nil {
		t.Fatal(err)
	}
	assertProblem(t, e.do(t, http.MethodGet, path, nil, map[string]string{"Authorization": "Bearer " + issue(other.ID)}), http.StatusNotFound, api.CodeWorkflowRunNotFound)
	if response := e.do(t, http.MethodGet, "/v1/workflows/runs/"+run.ID+"/diagnostics", nil, headers); response.Code != http.StatusForbidden {
		t.Fatalf("tenant token reached account diagnostics: %d %s", response.Code, response.Body)
	}
}
