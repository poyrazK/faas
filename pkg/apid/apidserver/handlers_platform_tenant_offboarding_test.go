package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPlatformTenantOffboardingPlanIsReadOnlyAndOwnershipAware(t *testing.T) {
	apidserver.WithTenantSurfacesEnabled(t)
	e := apidserver.Setup(t, api.PlanPro)
	appID := apidserver.MustSeedApp(t, e, "offboarding-plan")
	created := readApplyResponse(t, e.Do(t, http.MethodPost, "/v1/account/platform-tenants/apply", api.ApplyPlatformTenantRequest{
		ExternalRef: "offboard-customer", Name: "Offboard customer",
		Consumers: []api.ApplyPlatformTenantConsumerRequest{{AppID: appID, ExternalRef: "managed", Name: "Managed"}},
		Surfaces: []api.ApplyPlatformTenantSurfaceRequest{{AppID: appID, Name: "Managed surface",
			Hostnames: []string{"one.customer.example", "two.customer.example"}}},
	}, nil))
	if created.TenantID == "" || len(created.Consumers) != 1 || len(created.Surfaces) != 1 {
		t.Fatalf("initial tenant apply = %+v", created)
	}

	ctx := context.Background()
	unmanagedConsumer, err := (*e.Store()).CreateAPIConsumer(ctx, (*e.Acct()).ID, appID, "unmanaged", "Unmanaged")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (*e.Store()).LinkPlatformTenantConsumer(ctx, (*e.Acct()).ID, created.TenantID, unmanagedConsumer.ID); err != nil {
		t.Fatal(err)
	}
	unmanagedSurfaceID := apidserver.SeedTenantSurface(t, e, appID, "Unmanaged surface")
	if _, err := (*e.Store()).LinkPlatformTenantSurface(ctx, (*e.Acct()).ID, created.TenantID, unmanagedSurfaceID); err != nil {
		t.Fatal(err)
	}
	limits, ok := api.LimitsFor((*e.Acct()).Plan)
	if !ok {
		t.Fatal("missing account plan limits")
	}
	if _, err := (*e.Store()).CreateTenantHostnameIfUnderQuota(ctx, state.CreateTenantHostnameParams{
		SurfaceID: created.Surfaces[0].ID, Hostname: "legacy.customer.example", ChallengeToken: "legacy-challenge",
	}, limits); err != nil {
		t.Fatal(err)
	}

	_, keyPrefix, keyHash, err := api.GenerateConsumerKey()
	if err != nil {
		t.Fatal(err)
	}
	credentialStore := state.PlatformTenantCredentialStore((*e.Store()))
	if _, err := credentialStore.ApplyPlatformTenantCredentials(ctx, state.ApplyPlatformTenantCredentialsParams{
		AccountID: (*e.Acct()).ID, TenantID: created.TenantID, AppLimit: 100, AccountLimit: 100,
		Keys: []state.PlatformTenantCredentialIntent{{ConsumerID: created.Consumers[0].ID, Name: "customer-key",
			Prefix: keyPrefix, Hash: keyHash, Scopes: []string{"read"}}},
	}); err != nil {
		t.Fatal(err)
	}
	accessStore := state.PlatformTenantAccessStore((*e.Store()))
	_, tokenPrefix, tokenHash, err := api.GeneratePlatformTenantAccessToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := accessStore.CreatePlatformTenantAccessToken(ctx, state.PlatformTenantAccessTokenInput{
		AccountID: (*e.Acct()).ID, TenantID: created.TenantID, Name: "customer automation", Prefix: tokenPrefix,
		TokenHash: tokenHash, Scopes: []string{api.ScopePlatformTenantConsumersManage}, ExpiresAt: time.Now().UTC().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := state.PlatformTenantCredentialPolicyStore((*e.Store())).SetPlatformTenantCredentialPolicy(ctx, (*e.Acct()).ID, created.TenantID, []string{"read"}, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := state.PlatformTenantConsumerProvisioningPolicyStore((*e.Store())).SetPlatformTenantConsumerProvisioningPolicy(ctx, (*e.Acct()).ID, created.TenantID, true, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := state.PlatformTenantHostnamePolicyStore((*e.Store())).SetPlatformTenantHostnamePolicy(ctx, (*e.Acct()).ID, created.TenantID, []string{"customer.example"}, 5); err != nil {
		t.Fatal(err)
	}

	path := "/v1/account/platform-tenants/" + created.TenantID + "/offboarding-plan"
	readPlan := func() api.PlatformTenantOffboardingPlanResponse {
		t.Helper()
		resp := e.Do(t, http.MethodPost, path, struct{}{}, nil)
		if resp.Code != http.StatusOK || resp.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("offboarding plan = %d %s (cache-control %q)", resp.Code, resp.Body.String(), resp.Header().Get("Cache-Control"))
		}
		var plan api.PlatformTenantOffboardingPlanResponse
		if err := json.Unmarshal(resp.Body.Bytes(), &plan); err != nil {
			t.Fatal(err)
		}
		return plan
	}
	plan := readPlan()
	if plan.TenantID != created.TenantID || plan.Status != state.PlatformTenantActive || len(plan.PlanHash) != 64 {
		t.Fatalf("offboarding plan identity = %+v", plan)
	}
	actions := plan.Actions
	if !actions.SuspendTenant || actions.RevokeConsumerKeys != 1 || actions.RevokeAccessTokens != 1 ||
		actions.DetachManagedConsumers != 1 || actions.RetainUnmanagedConsumers != 1 ||
		actions.DetachManagedSurfaces != 1 || actions.RetainUnmanagedSurfaces != 1 ||
		actions.RemoveManagedHostnames != 2 || actions.RetainUnmanagedHostnames != 1 ||
		!actions.DisableCredentialDelegation || !actions.DisableCustomerProvisioning || !actions.DisableHostnameDelegation ||
		!actions.PreserveUsageHistory || !actions.PreserveBillingStatements || !actions.PreserveReconciliationHistory ||
		!actions.PreserveWebhookSubscriptions {
		t.Fatalf("offboarding actions = %+v", actions)
	}
	if repeated := readPlan(); repeated.PlanHash != plan.PlanHash || repeated.Actions != plan.Actions {
		t.Fatalf("offboarding plan changed without state change: first=%+v repeated=%+v", plan, repeated)
	}
	tenant, err := (*e.Store()).GetPlatformTenant(ctx, (*e.Acct()).ID, created.TenantID)
	if err != nil || tenant.Status != state.PlatformTenantActive {
		t.Fatalf("planning changed tenant status: %+v, %v", tenant, err)
	}
	keys, err := credentialStore.ListPlatformTenantCredentials(ctx, (*e.Acct()).ID, created.TenantID, 100, 0)
	if err != nil || len(keys) != 1 || keys[0].RevokedAt != nil {
		t.Fatalf("planning changed consumer credentials: %+v, %v", keys, err)
	}
	tokens, err := accessStore.ListPlatformTenantAccessTokens(ctx, (*e.Acct()).ID, created.TenantID)
	if err != nil || len(tokens) != 1 || tokens[0].RevokedAt != nil {
		t.Fatalf("planning changed access tokens: %+v, %v", tokens, err)
	}
}

func TestPlatformTenantOffboardingPlanHidesForeignTenant(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	other, _, err := (*e.Store()).CreatePlatformTenant(context.Background(), (*e.Acct()).ID, "another-offboard", "Another", 250)
	if err != nil {
		t.Fatal(err)
	}
	foreign := apidserver.Setup(t, api.PlanPro)
	resp := foreign.Do(t, http.MethodPost, "/v1/account/platform-tenants/"+other.ID+"/offboarding-plan", struct{}{}, nil)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("foreign offboarding plan status=%d, want 404: %s", resp.Code, resp.Body)
	}
}
