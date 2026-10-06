package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPlatformTenantSelfWorkflowRunHistoryIsTenantScopedAndPaged(t *testing.T) {
	e := setup(t, api.PlanHobby)
	ctx := context.Background()
	app := seedWorkflowApp(t, e, "tenant-workflow-run-history")
	required := true
	if _, err := e.store.UpdateApp(ctx, app.ID, state.UpdateAppParams{PlatformTenantRequired: &required, SetPlatformTenantRequired: true}); err != nil {
		t.Fatal(err)
	}
	tenants := e.store.(state.PlatformTenantStore)
	tenantA, _, err := tenants.CreatePlatformTenant(ctx, e.acct.ID, "run-history-customer-a", "Customer A", 10)
	if err != nil {
		t.Fatal(err)
	}
	tenantB, _, err := tenants.CreatePlatformTenant(ctx, e.acct.ID, "run-history-customer-b", "Customer B", 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, tenant := range []state.PlatformTenant{tenantA, tenantB} {
		consumer, err := e.store.CreateAPIConsumer(ctx, e.acct.ID, app.ID, tenant.ExternalRef, tenant.Name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tenants.LinkPlatformTenantConsumer(ctx, e.acct.ID, tenant.ID, consumer.ID); err != nil {
			t.Fatal(err)
		}
	}
	issueToken := func(tenant state.PlatformTenant, name string, scopes ...string) string {
		t.Helper()
		issued := e.do(t, http.MethodPost, "/v1/account/platform-tenants/"+tenant.ID+"/access-tokens",
			api.CreatePlatformTenantAccessTokenRequest{Name: name, Scopes: scopes}, nil)
		if issued.Code != http.StatusCreated {
			t.Fatalf("issue run-history token: %d %s", issued.Code, issued.Body)
		}
		var response api.CreatePlatformTenantAccessTokenResponse
		if err := json.Unmarshal(issued.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return response.Token
	}
	tokenA := issueToken(tenantA, "history reader a", api.ScopePlatformTenantInvocationsRead, api.ScopePlatformTenantInvocationsManage)
	tokenB := issueToken(tenantB, "history reader b", api.ScopePlatformTenantInvocationsRead, api.ScopePlatformTenantInvocationsManage)
	manageOnly := issueToken(tenantA, "history manager", api.ScopePlatformTenantInvocationsManage)
	start := func(token, orderID string) api.WorkflowRunResponse {
		t.Helper()
		created := e.do(t, http.MethodPost, "/v1/platform-tenant-self/apps/"+app.Slug+"/workflows/process-order/runs",
			map[string]string{"order_id": orderID}, map[string]string{"Authorization": "Bearer " + token})
		if created.Code != http.StatusCreated {
			t.Fatalf("start tenant workflow: %d %s", created.Code, created.Body)
		}
		var run api.WorkflowRunResponse
		if err := json.Unmarshal(created.Body.Bytes(), &run); err != nil {
			t.Fatal(err)
		}
		return run
	}
	a1 := start(tokenA, "a-1")
	_ = start(tokenA, "a-2")
	b1 := start(tokenB, "b-1")
	if err := e.store.MarkWorkflowRunStatus(ctx, a1.ID, state.WorkflowRunStatusSucceeded, json.RawMessage(`{"done":true}`), nil); err != nil {
		t.Fatal(err)
	}

	path := "/v1/platform-tenant-self/apps/" + app.Slug + "/workflows/runs"
	listed := e.do(t, http.MethodGet, path, nil, map[string]string{"Authorization": "Bearer " + tokenA})
	var response api.ListWorkflowRunsResponse
	if listed.Code != http.StatusOK || listed.Header().Get("Cache-Control") != "no-store" || json.Unmarshal(listed.Body.Bytes(), &response) != nil {
		t.Fatalf("list tenant workflow history: %d %s", listed.Code, listed.Body)
	}
	if response.Total != 2 || len(response.Runs) != 2 {
		t.Fatalf("tenant A history total=%d runs=%+v", response.Total, response.Runs)
	}
	seenA := map[string]bool{}
	for _, run := range response.Runs {
		if run.PlatformTenantID != tenantA.ID || run.AppID != app.ID {
			t.Fatalf("tenant A received another tenant's run: %+v", run)
		}
		seenA[run.ID] = true
	}
	if !seenA[a1.ID] {
		t.Fatalf("tenant A history omitted its known run %s: %+v", a1.ID, response.Runs)
	}

	filtered := e.do(t, http.MethodGet, path+"?status=pending&workflow_name=process-order&limit=1&offset=0", nil,
		map[string]string{"Authorization": "Bearer " + tokenA})
	if filtered.Code != http.StatusOK || json.Unmarshal(filtered.Body.Bytes(), &response) != nil || response.Total != 1 || len(response.Runs) != 1 || response.Runs[0].Status != state.WorkflowRunStatusPending {
		t.Fatalf("filtered tenant run history: %d %s", filtered.Code, filtered.Body)
	}

	listedB := e.do(t, http.MethodGet, path, nil, map[string]string{"Authorization": "Bearer " + tokenB})
	if listedB.Code != http.StatusOK || json.Unmarshal(listedB.Body.Bytes(), &response) != nil || response.Total != 1 || len(response.Runs) != 1 ||
		response.Runs[0].ID != b1.ID || response.Runs[0].PlatformTenantID != tenantB.ID {
		t.Fatalf("tenant B history: %d %s", listedB.Code, listedB.Body)
	}
	if denied := e.do(t, http.MethodGet, path, nil, map[string]string{"Authorization": "Bearer " + manageOnly}); denied.Code != http.StatusForbidden {
		t.Fatalf("manage-only token listed workflow history: %d %s", denied.Code, denied.Body)
	}
	if invalid := e.do(t, http.MethodGet, path+"?status=unknown", nil, map[string]string{"Authorization": "Bearer " + tokenA}); invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid workflow status filter: %d %s", invalid.Code, invalid.Body)
	}
}
