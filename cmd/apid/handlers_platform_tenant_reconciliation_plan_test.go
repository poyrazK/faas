package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPlatformTenantReconciliationPlanIsReadOnlyAndOwnershipAware(t *testing.T) {
	t.Setenv("FAAS_TENANT_SURFACES_ENABLED", "true")
	e := setup(t, api.PlanPro)
	appID := mustSeedApp(t, e, "reconciliation-plan")
	initial := api.ApplyPlatformTenantRequest{ExternalRef: "plan-customer", Name: "Plan Customer",
		Consumers: []api.ApplyPlatformTenantConsumerRequest{
			{AppID: appID, ExternalRef: "managed-keep", Name: "Managed Keep"},
			{AppID: appID, ExternalRef: "managed-remove", Name: "Managed Remove"},
		},
		Surfaces: []api.ApplyPlatformTenantSurfaceRequest{{AppID: appID, Name: "Managed surface",
			Hostnames: []string{"keep.example.com", "remove.example.com"}}}}
	applied := readApplyResponse(t, e.do(t, http.MethodPost, "/v1/account/platform-tenants/apply", initial, nil))
	if applied.TenantID == "" || len(applied.Surfaces) != 1 || applied.Surfaces[0].ID == "" {
		t.Fatalf("initial apply = %+v", applied)
	}

	ctx := context.Background()
	unmanagedConsumer, err := e.store.CreateAPIConsumer(ctx, e.acct.ID, appID, "unmanaged", "Unmanaged")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.LinkPlatformTenantConsumer(ctx, e.acct.ID, applied.TenantID, unmanagedConsumer.ID); err != nil {
		t.Fatal(err)
	}
	limits, ok := api.LimitsFor(e.acct.Plan)
	if !ok {
		t.Fatal("missing account plan limits")
	}
	if _, err := e.store.CreateTenantHostnameIfUnderQuota(ctx, state.CreateTenantHostnameParams{
		SurfaceID: applied.Surfaces[0].ID, Hostname: "legacy.example.com", ChallengeToken: "legacy-token",
	}, limits); err != nil {
		t.Fatal(err)
	}
	unmanagedSurfaceID := seedTenantSurface(t, e, appID, "Legacy surface")
	if _, err := e.store.LinkPlatformTenantSurface(ctx, e.acct.ID, applied.TenantID, unmanagedSurfaceID); err != nil {
		t.Fatal(err)
	}

	request := api.PlanPlatformTenantReconciliationRequest{
		Consumers: []api.ApplyPlatformTenantConsumerRequest{{AppID: appID, ExternalRef: "managed-keep", Name: "Managed Keep"},
			{AppID: appID, ExternalRef: "new-consumer", Name: "New Consumer"}},
		Surfaces: []api.ApplyPlatformTenantSurfaceRequest{
			{AppID: appID, Name: "Managed surface", Hostnames: []string{"keep.example.com"}},
			{AppID: appID, Name: "New surface", Hostnames: []string{"new.example.com"}},
		},
	}
	path := "/v1/account/platform-tenants/" + applied.TenantID + "/reconciliation-plan"
	readPlan := func() api.PlatformTenantReconciliationPlanResponse {
		t.Helper()
		resp := e.do(t, http.MethodPost, path, request, nil)
		if resp.Code != http.StatusOK {
			t.Fatalf("plan = %d %s", resp.Code, resp.Body.String())
		}
		var plan api.PlatformTenantReconciliationPlanResponse
		if err := json.Unmarshal(resp.Body.Bytes(), &plan); err != nil {
			t.Fatal(err)
		}
		return plan
	}
	plan := readPlan()
	if plan.TenantID != applied.TenantID {
		t.Fatalf("plan tenant = %q, want %q", plan.TenantID, applied.TenantID)
	}
	assertChange := func(resourceType, action, externalRef, hostname, id string, managed *bool) {
		t.Helper()
		for _, change := range plan.Changes {
			if change.ResourceType == resourceType && change.Action == action && change.ExternalRef == externalRef &&
				change.Hostname == hostname && (id == "" || change.ID == id) {
				if managed == nil && change.ManagedByPlatformTenant != nil {
					t.Fatalf("change %+v unexpectedly reports existing ownership", change)
				}
				if managed != nil && (change.ManagedByPlatformTenant == nil || *change.ManagedByPlatformTenant != *managed) {
					t.Fatalf("change %+v managed_by_platform_tenant want %t", change, *managed)
				}
				return
			}
		}
		t.Fatalf("missing plan change type=%s action=%s external_ref=%s hostname=%s id=%s: %+v",
			resourceType, action, externalRef, hostname, id, plan.Changes)
	}
	managed, unmanaged := true, false
	assertChange("consumer", "keep", "managed-keep", "", "", &managed)
	assertChange("consumer", "create", "new-consumer", "", "", nil)
	assertChange("consumer", "remove_candidate", "managed-remove", "", "", &managed)
	assertChange("consumer", "retain_unmanaged", "unmanaged", "", "", &unmanaged)
	assertChange("surface", "keep", "", "", applied.Surfaces[0].ID, &managed)
	assertChange("surface", "create", "", "", "", nil)
	assertChange("surface", "retain_unmanaged", "", "", unmanagedSurfaceID, &unmanaged)
	assertChange("hostname", "keep", "", "keep.example.com", "", &managed)
	assertChange("hostname", "remove_candidate", "", "remove.example.com", "", &managed)
	assertChange("hostname", "retain_unmanaged", "", "legacy.example.com", "", &unmanaged)
	assertChange("hostname", "create", "", "new.example.com", "", nil)
	if repeated := readPlan(); !equalPlatformTenantPlanChanges(plan.Changes, repeated.Changes) {
		t.Fatalf("plan is not deterministic:\nfirst:  %+v\nsecond: %+v", plan.Changes, repeated.Changes)
	}

	consumers, err := e.store.ListPlatformTenantConsumers(ctx, e.acct.ID, applied.TenantID)
	if err != nil || len(consumers) != 3 {
		t.Fatalf("planning changed consumers: %+v, %v", consumers, err)
	}
	surfaces, err := e.store.ListPlatformTenantSurfaces(ctx, e.acct.ID, applied.TenantID)
	if err != nil || len(surfaces) != 2 {
		t.Fatalf("planning changed surfaces: %+v, %v", surfaces, err)
	}
	hostnames, err := e.store.ListTenantHostnamesForSurface(ctx, applied.Surfaces[0].ID)
	if err != nil || len(hostnames) != 3 {
		t.Fatalf("planning changed hostnames: %+v, %v", hostnames, err)
	}
}

func equalPlatformTenantPlanChanges(a, b []api.PlatformTenantReconciliationPlanChange) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ResourceType != b[i].ResourceType || a[i].Action != b[i].Action || a[i].ID != b[i].ID ||
			a[i].AppID != b[i].AppID || a[i].ExternalRef != b[i].ExternalRef || a[i].Name != b[i].Name ||
			a[i].SurfaceID != b[i].SurfaceID || a[i].Hostname != b[i].Hostname ||
			!equalOptionalBool(a[i].ManagedByPlatformTenant, b[i].ManagedByPlatformTenant) {
			return false
		}
	}
	return true
}

func equalOptionalBool(a, b *bool) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func TestPlatformTenantReconciliationPlanDoesNotAcceptCrossAccountTenant(t *testing.T) {
	e := setup(t, api.PlanPro)
	other, err := e.store.CreateAccount(context.Background(), "reconcile-plan-other@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	foreign, _, err := e.store.CreatePlatformTenant(context.Background(), other.ID, "foreign-plan", "Foreign plan", 100)
	if err != nil {
		t.Fatal(err)
	}
	resp := e.do(t, http.MethodPost, "/v1/account/platform-tenants/"+foreign.ID+"/reconciliation-plan",
		api.PlanPlatformTenantReconciliationRequest{}, nil)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("cross-account plan = %d %s", resp.Code, resp.Body.String())
	}
}
