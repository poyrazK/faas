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

func TestPlatformTenantReconciliationApplyIsConfirmedAtomicAndOwnershipAware(t *testing.T) {
	t.Setenv("FAAS_TENANT_SURFACES_ENABLED", "true")
	e := setup(t, api.PlanPro)
	appID := mustSeedApp(t, e, "reconciliation-apply")
	initial := api.ApplyPlatformTenantRequest{ExternalRef: "apply-customer", Name: "Apply Customer",
		Consumers: []api.ApplyPlatformTenantConsumerRequest{
			{AppID: appID, ExternalRef: "managed-keep", Name: "Managed Keep"},
			{AppID: appID, ExternalRef: "managed-remove", Name: "Managed Remove"},
		},
		Surfaces: []api.ApplyPlatformTenantSurfaceRequest{
			{AppID: appID, Name: "Managed Keep Surface", Hostnames: []string{"keep.example.com", "remove.example.com"}},
			{AppID: appID, Name: "Managed Remove Surface", Hostnames: []string{"orphan.example.com"}},
		}}
	created := readApplyResponse(t, e.do(t, http.MethodPost, "/v1/account/platform-tenants/apply", initial, nil))
	tenantID := created.TenantID
	keepSurfaceID, removeSurfaceID := created.Surfaces[0].ID, created.Surfaces[1].ID

	ctx := context.Background()
	unmanagedConsumer, err := e.store.CreateAPIConsumer(ctx, e.acct.ID, appID, "unmanaged", "Unmanaged")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.LinkPlatformTenantConsumer(ctx, e.acct.ID, tenantID, unmanagedConsumer.ID); err != nil {
		t.Fatal(err)
	}
	limits, ok := api.LimitsFor(e.acct.Plan)
	if !ok {
		t.Fatal("missing account plan limits")
	}
	if _, err := e.store.CreateTenantHostnameIfUnderQuota(ctx, state.CreateTenantHostnameParams{
		SurfaceID: keepSurfaceID, Hostname: "legacy.example.com", ChallengeToken: "legacy-token",
	}, limits); err != nil {
		t.Fatal(err)
	}
	unmanagedSurfaceID := seedTenantSurface(t, e, appID, "Unmanaged Surface")
	if _, err := e.store.LinkPlatformTenantSurface(ctx, e.acct.ID, tenantID, unmanagedSurfaceID); err != nil {
		t.Fatal(err)
	}

	desired := api.PlanPlatformTenantReconciliationRequest{
		Consumers: []api.ApplyPlatformTenantConsumerRequest{
			{AppID: appID, ExternalRef: "managed-keep", Name: "Managed Keep"},
			{AppID: appID, ExternalRef: "new-consumer", Name: "New Consumer"},
		},
		Surfaces: []api.ApplyPlatformTenantSurfaceRequest{
			{AppID: appID, Name: "Managed Keep Surface", Hostnames: []string{"keep.example.com"}},
			{AppID: appID, Name: "New Surface", Hostnames: []string{"new.example.com"}},
		},
	}
	planPath := "/v1/account/platform-tenants/" + tenantID + "/reconciliation-plan"
	planResp := e.do(t, http.MethodPost, planPath, desired, nil)
	if planResp.Code != http.StatusOK {
		t.Fatalf("plan = %d %s", planResp.Code, planResp.Body.String())
	}
	var plan api.PlatformTenantReconciliationPlanResponse
	if err := json.Unmarshal(planResp.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.PlanHash) != 64 {
		t.Fatalf("plan hash = %q, want 64 hex chars", plan.PlanHash)
	}

	applyReq := api.ApplyPlatformTenantReconciliationRequest{Consumers: desired.Consumers, SurfaceIDs: desired.SurfaceIDs,
		Surfaces: desired.Surfaces, ExpectedPlanHash: plan.PlanHash}
	applyPath := planPath + "/apply"
	key := map[string]string{"Idempotency-Key": "apply-reconcile-1"}
	applyResp := e.do(t, http.MethodPost, applyPath, applyReq, key)
	if applyResp.Code != http.StatusOK {
		t.Fatalf("apply = %d %s", applyResp.Code, applyResp.Body.String())
	}
	var applied api.PlatformTenantReconciliationApplyResponse
	if err := json.Unmarshal(applyResp.Body.Bytes(), &applied); err != nil {
		t.Fatal(err)
	}
	if !applied.Applied || applied.PlanHash != plan.PlanHash || applied.TenantID != tenantID {
		t.Fatalf("apply response = %+v", applied)
	}
	assertAppliedChange := func(resourceType, action, externalRef, hostname, name string) api.PlatformTenantReconciliationPlanChange {
		t.Helper()
		for _, change := range applied.Changes {
			if change.ResourceType == resourceType && change.Action == action && change.ExternalRef == externalRef &&
				change.Hostname == hostname && change.Name == name {
				return change
			}
		}
		t.Fatalf("missing applied change type=%s action=%s ref=%s hostname=%s name=%s: %+v", resourceType, action, externalRef, hostname, name, applied.Changes)
		return api.PlatformTenantReconciliationPlanChange{}
	}
	newConsumer := assertAppliedChange("consumer", "created", "new-consumer", "", "New Consumer")
	if newConsumer.ID == "" {
		t.Fatal("created consumer has no ID")
	}
	if removedConsumer := assertAppliedChange("consumer", "detached", "managed-remove", "", "Managed Remove"); removedConsumer.ID == "" {
		t.Fatal("detached consumer has no ID")
	}
	newSurface := assertAppliedChange("surface", "created", "", "", "New Surface")
	if newSurface.ID == "" {
		t.Fatal("created surface has no ID")
	}
	newHostname := assertAppliedChange("hostname", "created", "", "new.example.com", "New Surface")
	if newHostname.ID == "" || newHostname.SurfaceID != newSurface.ID {
		t.Fatalf("created hostname = %+v, surface = %+v", newHostname, newSurface)
	}
	assertAppliedChange("hostname", "removed", "", "remove.example.com", "Managed Keep Surface")
	assertAppliedChange("surface", "detached", "", "", "Managed Remove Surface")

	consumers, err := e.store.ListPlatformTenantConsumers(ctx, e.acct.ID, tenantID)
	if err != nil || len(consumers) != 3 {
		t.Fatalf("linked consumers after apply = %+v, %v; want keep, new, and unmanaged", consumers, err)
	}
	surfaces, err := e.store.ListPlatformTenantSurfaces(ctx, e.acct.ID, tenantID)
	if err != nil || len(surfaces) != 3 {
		t.Fatalf("linked surfaces after apply = %+v, %v; want keep, new, and unmanaged", surfaces, err)
	}
	keepHosts, err := e.store.ListTenantHostnamesForSurface(ctx, keepSurfaceID)
	if err != nil || len(keepHosts) != 2 {
		t.Fatalf("keep surface hostnames = %+v, %v; want kept and unmanaged hostnames", keepHosts, err)
	}
	removedSurfaceHosts, err := e.store.ListTenantHostnamesForSurface(ctx, removeSurfaceID)
	if err != nil || len(removedSurfaceHosts) != 1 {
		t.Fatalf("detached surface hostnames = %+v, %v; want underlying resource retained", removedSurfaceHosts, err)
	}
	newHosts, err := e.store.ListTenantHostnamesForSurface(ctx, newSurface.ID)
	if err != nil || len(newHosts) != 1 || newHosts[0].Hostname != "new.example.com" {
		t.Fatalf("new surface hostnames = %+v, %v", newHosts, err)
	}

	replay := e.do(t, http.MethodPost, applyPath, applyReq, key)
	if replay.Code != http.StatusOK || replay.Header().Get("Idempotent-Replayed") != "true" || replay.Body.String() != applyResp.Body.String() {
		t.Fatalf("idempotent replay = %d replay=%q body=%s", replay.Code, replay.Header().Get("Idempotent-Replayed"), replay.Body.String())
	}
}

func TestPlatformTenantReconciliationApplyRejectsStalePlanWithoutWrites(t *testing.T) {
	e := setup(t, api.PlanPro)
	appID := mustSeedApp(t, e, "reconciliation-stale")
	created := readApplyResponse(t, e.do(t, http.MethodPost, "/v1/account/platform-tenants/apply",
		api.ApplyPlatformTenantRequest{ExternalRef: "stale-customer", Name: "Stale Customer",
			Consumers: []api.ApplyPlatformTenantConsumerRequest{{AppID: appID, ExternalRef: "managed", Name: "Managed"}}}, nil))
	path := "/v1/account/platform-tenants/" + created.TenantID + "/reconciliation-plan"
	planResp := e.do(t, http.MethodPost, path, api.PlanPlatformTenantReconciliationRequest{}, nil)
	if planResp.Code != http.StatusOK {
		t.Fatalf("plan = %d %s", planResp.Code, planResp.Body.String())
	}
	var plan api.PlatformTenantReconciliationPlanResponse
	if err := json.Unmarshal(planResp.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	other, err := e.store.CreateAPIConsumer(ctx, e.acct.ID, appID, "joined-after-preview", "Joined After Preview")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.LinkPlatformTenantConsumer(ctx, e.acct.ID, created.TenantID, other.ID); err != nil {
		t.Fatal(err)
	}
	resp := e.do(t, http.MethodPost, path+"/apply", api.ApplyPlatformTenantReconciliationRequest{ExpectedPlanHash: plan.PlanHash},
		map[string]string{"Idempotency-Key": "apply-stale-plan-1"})
	if resp.Code != http.StatusConflict || !strings.Contains(resp.Body.String(), "platform_tenant_plan_stale") {
		t.Fatalf("stale apply = %d %s", resp.Code, resp.Body.String())
	}
	consumers, err := e.store.ListPlatformTenantConsumers(ctx, e.acct.ID, created.TenantID)
	if err != nil || len(consumers) != 2 {
		t.Fatalf("stale apply changed linked consumers = %+v, %v", consumers, err)
	}
}
