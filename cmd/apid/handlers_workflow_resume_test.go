package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWorkflowResumeAPIIdempotencyOwnershipAndHistory(t *testing.T) {
	e := setup(t, api.PlanHobby)
	app := seedWorkflowApp(t, e, "resume-api")
	ctx := context.Background()
	response := e.do(t, "POST", "/v1/apps/"+app.Slug+"/workflows/w1/runs", map[string]any{"invoice": 42}, nil)
	var created api.WorkflowRunResponse
	if response.Code != http.StatusCreated || json.Unmarshal(response.Body.Bytes(), &created) != nil {
		t.Fatalf("create=%d %s", response.Code, response.Body.String())
	}
	if err := e.store.CreateWorkflowSteps(ctx, created.ID, []*state.WorkflowStep{{StepName: "main"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.ClaimNextDueWorkflowRun(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.StartWorkflowStep(ctx, created.ID, "main", 1, json.RawMessage(`{"invoice":42}`)); err != nil {
		t.Fatal(err)
	}
	message := "provider unavailable"
	code := 503
	if err := e.store.MarkWorkflowStepAttemptStatus(ctx, created.ID, "main", state.WorkflowStepStatusDead, 1, &code, nil, &message); err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkWorkflowRunStatus(ctx, created.ID, state.WorkflowRunStatusDead, nil, &message); err != nil {
		t.Fatal(err)
	}
	path := "/v1/workflows/runs/" + created.ID
	for _, body := range []any{map[string]any{}, map[string]any{"expected_resume_count": -1}, map[string]any{"expected_resume_count": nil}, map[string]any{"expected_resume_count": 0, "unknown": true}} {
		response = e.do(t, "POST", path+"/resume", body, nil)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid resume=%d %s", response.Code, response.Body.String())
		}
	}
	response = e.do(t, "POST", path+"/resume", map[string]any{"expected_resume_count": 0, "padding": strings.Repeat("x", int(api.WorkflowResumeRequestMaxBytes))}, nil)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized resume=%d %s", response.Code, response.Body.String())
	}
	body := map[string]any{"expected_resume_count": 0}
	headers := map[string]string{"Idempotency-Key": "resume-once"}
	response = e.do(t, "POST", path+"/resume", body, headers)
	var resumed api.WorkflowRunResponse
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &resumed) != nil || resumed.ResumeCount != 1 || resumed.Status != state.WorkflowRunStatusPending {
		t.Fatalf("resume=%d %s", response.Code, response.Body.String())
	}
	response = e.do(t, "POST", path+"/resume", body, headers)
	if response.Code != http.StatusOK {
		t.Fatalf("idempotent replay=%d %s", response.Code, response.Body.String())
	}
	response = e.do(t, "POST", path+"/resume", body, nil)
	if response.Code != http.StatusConflict {
		t.Fatalf("stale resume=%d %s", response.Code, response.Body.String())
	}
	response = e.do(t, "GET", path+"/resumes", nil, nil)
	var history api.ListWorkflowResumesResponse
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &history) != nil || len(history.Resumes) != 1 || history.Resumes[0].AccountID != app.AccountID || history.Resumes[0].PreviousStatus != state.WorkflowRunStatusDead || history.Resumes[0].ResumeNumber != 1 {
		t.Fatalf("history=%d %s", response.Code, response.Body.String())
	}
	response = e.do(t, "GET", path+"/steps", nil, nil)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"retry_base":1`) {
		t.Fatalf("step budget absent=%d %s", response.Code, response.Body.String())
	}
	// A distinct authenticated account sees neither the run nor its history.
	other, err := e.store.CreateAccount(ctx, "other-resume@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	token, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateAPIKey(ctx, other.ID, hash, "other", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	for _, route := range []struct {
		method, path string
		body         any
	}{{"POST", path + "/resume", body}, {"GET", path + "/resumes", nil}} {
		response = e.do(t, route.method, route.path, route.body, map[string]string{"Authorization": "Bearer " + token})
		if response.Code != http.StatusNotFound {
			t.Fatalf("cross-account request=%d %s", response.Code, response.Body.String())
		}
	}
}

func TestPlatformTenantSelfWorkflowResume(t *testing.T) {
	e := setup(t, api.PlanHobby)
	app := seedWorkflowApp(t, e, "tenant-resume-api")
	required := true
	if _, err := e.store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{PlatformTenantRequired: &required, SetPlatformTenantRequired: true}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	tenant, _, err := e.store.CreatePlatformTenant(ctx, e.acct.ID, "resume-customer", "Resume customer", 10)
	if err != nil {
		t.Fatal(err)
	}
	seedPlatformTenantConsumer(t, e, app.Slug, tenant.ID)
	issueToken := func(tenantID, name string) string {
		t.Helper()
		issued := e.do(t, http.MethodPost, "/v1/account/platform-tenants/"+tenantID+"/access-tokens", api.CreatePlatformTenantAccessTokenRequest{
			Name: name, Scopes: []string{api.ScopePlatformTenantInvocationsManage},
		}, nil)
		if issued.Code != http.StatusCreated {
			t.Fatalf("create tenant token: %d %s", issued.Code, issued.Body)
		}
		var token api.CreatePlatformTenantAccessTokenResponse
		if err := json.Unmarshal(issued.Body.Bytes(), &token); err != nil {
			t.Fatal(err)
		}
		return token.Token
	}
	token := issueToken(tenant.ID, "resume workflow")
	otherTenant, _, err := e.store.CreatePlatformTenant(ctx, e.acct.ID, "other-resume-customer", "Other resume customer", 10)
	if err != nil {
		t.Fatal(err)
	}
	otherToken := issueToken(otherTenant.ID, "other workflow")

	created := e.do(t, http.MethodPost, "/v1/platform-tenant-self/apps/"+app.Slug+"/workflows/w1/runs", map[string]any{"invoice": 42}, map[string]string{"Authorization": "Bearer " + token})
	var run api.WorkflowRunResponse
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &run) != nil || run.PlatformTenantID != tenant.ID {
		t.Fatalf("create tenant run=%d %s", created.Code, created.Body)
	}
	if err := e.store.CreateWorkflowSteps(ctx, run.ID, []*state.WorkflowStep{{StepName: "main", Input: json.RawMessage(`{"invoice":42}`)}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.ClaimNextDueWorkflowRun(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.StartWorkflowStep(ctx, run.ID, "main", 1, json.RawMessage(`{"invoice":42}`)); err != nil {
		t.Fatal(err)
	}
	message := "provider unavailable"
	status := http.StatusServiceUnavailable
	if err := e.store.MarkWorkflowStepAttemptStatus(ctx, run.ID, "main", state.WorkflowStepStatusDead, 1, &status, nil, &message); err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkWorkflowRunStatus(ctx, run.ID, state.WorkflowRunStatusDead, nil, &message); err != nil {
		t.Fatal(err)
	}
	path := "/v1/platform-tenant-self/workflows/runs/" + run.ID + "/resume"
	body := map[string]any{"expected_resume_count": 0}
	foreign := e.do(t, http.MethodPost, path, body, map[string]string{"Authorization": "Bearer " + otherToken})
	assertProblem(t, foreign, http.StatusNotFound, api.CodeWorkflowRunNotFound)

	headers := map[string]string{"Authorization": "Bearer " + token, "Idempotency-Key": "tenant-resume-once"}
	resumed := e.do(t, http.MethodPost, path, body, headers)
	var response api.WorkflowRunResponse
	if resumed.Code != http.StatusOK || resumed.Header().Get("Cache-Control") != "no-store" || json.Unmarshal(resumed.Body.Bytes(), &response) != nil || response.Status != state.WorkflowRunStatusPending || response.ResumeCount != 1 || response.PlatformTenantID != tenant.ID {
		t.Fatalf("tenant resume=%d %s", resumed.Code, resumed.Body)
	}
	replayed := e.do(t, http.MethodPost, path, body, headers)
	if replayed.Code != http.StatusOK || replayed.Body.String() != resumed.Body.String() {
		t.Fatalf("idempotent tenant resume=%d %s", replayed.Code, replayed.Body)
	}
	stale := e.do(t, http.MethodPost, path, body, map[string]string{"Authorization": "Bearer " + token})
	assertProblem(t, stale, http.StatusConflict, api.CodeWorkflowResumeConflict)
}
