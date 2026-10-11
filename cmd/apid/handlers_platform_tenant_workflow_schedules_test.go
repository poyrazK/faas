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

func TestPlatformTenantSelfWorkflowScheduleOverrides(t *testing.T) {
	e := setup(t, api.PlanPro)
	ctx := context.Background()
	app, err := e.store.CreateApp(ctx, state.App{AccountID: e.acct.ID, Slug: "tenant-configurable-schedule",
		Type: state.AppTypeFunction, Runtime: "node22", RAMMB: 256, PlatformTenantRequired: true})
	if err != nil {
		t.Fatal(err)
	}
	definitions, err := json.Marshal([]api.WorkflowSpec{
		{Name: "nightly", Trigger: &api.WorkflowTriggerSpec{Type: "schedule", Schedule: "0 7 * * *", Timezone: "UTC", TenantConfigurable: true, CatchUp: "latest", CatchUpWindow: "2h"},
			Steps: []api.WorkflowStepSpec{{Name: "report", Run: "report"}}},
		{Name: "owner-only", Trigger: &api.WorkflowTriggerSpec{Type: "schedule", Schedule: "0 8 * * *", Timezone: "UTC"},
			Steps: []api.WorkflowStepSpec{{Name: "report", Run: "report"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := e.store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage,
		ImageDigest: "registry.example.com/tenant-schedule@sha256:" + strings.Repeat("c", 64), Status: state.DeployPending,
		Workflows: definitions})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
		t.Fatal(err)
	}
	tenant, _, err := e.store.CreatePlatformTenant(ctx, e.acct.ID, "tenant-schedule-customer", "Tenant schedule customer", 10)
	if err != nil {
		t.Fatal(err)
	}
	seedPlatformTenantConsumer(t, e, app.Slug, tenant.ID)
	issueToken := func(name string, scopes ...string) string {
		t.Helper()
		issued := e.do(t, http.MethodPost, "/v1/account/platform-tenants/"+tenant.ID+"/access-tokens",
			api.CreatePlatformTenantAccessTokenRequest{Name: name, Scopes: scopes}, nil)
		if issued.Code != http.StatusCreated {
			t.Fatalf("issue schedule token: %d %s", issued.Code, issued.Body)
		}
		var response api.CreatePlatformTenantAccessTokenResponse
		if err := json.Unmarshal(issued.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return response.Token
	}
	manageToken := issueToken("schedule manager", api.ScopePlatformTenantAutomationsManage, api.ScopePlatformTenantAutomationsRead)
	readToken := issueToken("schedule reader", api.ScopePlatformTenantAutomationsRead)
	manageHeaders := map[string]string{"Authorization": "Bearer " + manageToken}
	readHeaders := map[string]string{"Authorization": "Bearer " + readToken}
	listPath := "/v1/platform-tenant-self/apps/" + app.Slug + "/workflows/schedules"
	listed := e.do(t, http.MethodGet, listPath, nil, manageHeaders)
	var schedules api.ListTenantWorkflowSchedulesResponse
	if listed.Code != http.StatusOK || json.Unmarshal(listed.Body.Bytes(), &schedules) != nil || len(schedules.Schedules) != 1 {
		t.Fatalf("list tenant schedules: %d %s", listed.Code, listed.Body)
	}
	initial := schedules.Schedules[0]
	if initial.WorkflowName != "nightly" || initial.Version != 0 || initial.Customized || initial.Schedule != "0 7 * * *" || !initial.TenantConfigurable || initial.CatchUp != "latest" || initial.CatchUpWindow != "2h0m0s" {
		t.Fatalf("initial tenant schedule=%+v", initial)
	}
	namePath := listPath + "/nightly"
	update := api.UpdateTenantWorkflowScheduleRequest{ExpectedVersion: int64Pointer(0), Schedule: "0 9 * * *",
		Timezone: "Europe/Istanbul", Overlap: "allow"}
	updated := e.do(t, http.MethodPut, namePath, update, manageHeaders)
	var response api.TenantWorkflowScheduleResponse
	if updated.Code != http.StatusOK || json.Unmarshal(updated.Body.Bytes(), &response) != nil || response.Version != 1 ||
		!response.Enabled || !response.Customized || response.Timezone != "Europe/Istanbul" || response.Overlap != "allow" || response.CatchUp != "latest" || response.CatchUpWindow != "2h0m0s" {
		t.Fatalf("update tenant schedule: %d %s", updated.Code, updated.Body)
	}
	stale := e.do(t, http.MethodPut, namePath, update, manageHeaders)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale tenant schedule update=%d %s", stale.Code, stale.Body)
	}
	for _, option := range []string{"catch_up", "catch_up_window"} {
		body := map[string]any{"expected_version": 1, "schedule": "0 9 * * *", option: "skip"}
		if denied := e.do(t, http.MethodPut, namePath, body, manageHeaders); denied.Code != http.StatusBadRequest {
			t.Fatalf("tenant changed owner recovery option %s: %d %s", option, denied.Code, denied.Body)
		}
	}
	ownerOnly := e.do(t, http.MethodPut, listPath+"/owner-only", update, manageHeaders)
	if ownerOnly.Code != http.StatusNotFound {
		t.Fatalf("non-opted-in schedule update=%d %s", ownerOnly.Code, ownerOnly.Body)
	}
	if denied := e.do(t, http.MethodPut, namePath, update, readHeaders); denied.Code != http.StatusForbidden {
		t.Fatalf("read-only tenant changed schedule: %d %s", denied.Code, denied.Body)
	}
	if denied := e.do(t, http.MethodGet, listPath, nil, map[string]string{"Authorization": "Bearer " + issueToken("manage only", api.ScopePlatformTenantAutomationsManage)}); denied.Code != http.StatusForbidden {
		t.Fatalf("manage-only token listed schedules: %d %s", denied.Code, denied.Body)
	}
	listed = e.do(t, http.MethodGet, listPath, nil, readHeaders)
	if listed.Code != http.StatusOK || json.Unmarshal(listed.Body.Bytes(), &schedules) != nil ||
		len(schedules.Schedules) != 1 || schedules.Schedules[0].Version != 1 || schedules.Schedules[0].Schedule != "0 9 * * *" {
		t.Fatalf("read updated tenant schedule: %d %s", listed.Code, listed.Body)
	}
}

func int64Pointer(value int64) *int64 { return &value }
