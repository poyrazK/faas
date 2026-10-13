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

func TestPlatformTenantOffboardingApplyIsAtomicAndRecoverable(t *testing.T) {
	apidserver.WithTenantSurfacesEnabled(t)
	e := apidserver.Setup(t, api.PlanPro)
	appID := apidserver.MustSeedApp(t, e, "offboarding-apply")
	created := readApplyResponse(t, e.Do(t, http.MethodPost, "/v1/account/platform-tenants/apply", api.ApplyPlatformTenantRequest{
		ExternalRef: "offboard-apply", Name: "Offboard apply",
		Consumers: []api.ApplyPlatformTenantConsumerRequest{{AppID: appID, ExternalRef: "managed", Name: "Managed"}},
		Surfaces: []api.ApplyPlatformTenantSurfaceRequest{{AppID: appID, Name: "Managed surface",
			Hostnames: []string{"managed.customer.example", "second.customer.example"}}},
	}, nil))
	if len(created.Consumers) != 1 || len(created.Surfaces) != 1 {
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
	keyResult, err := credentialStore.ApplyPlatformTenantCredentials(ctx, state.ApplyPlatformTenantCredentialsParams{
		AccountID: (*e.Acct()).ID, TenantID: created.TenantID, AppLimit: 100, AccountLimit: 100,
		Keys: []state.PlatformTenantCredentialIntent{{ConsumerID: created.Consumers[0].ID, Name: "customer-key",
			Prefix: keyPrefix, Hash: keyHash, Scopes: []string{"read"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, tokenPrefix, tokenHash, err := api.GeneratePlatformTenantAccessToken()
	if err != nil {
		t.Fatal(err)
	}
	accessStore := state.PlatformTenantAccessStore((*e.Store()))
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

	planPath := "/v1/account/platform-tenants/" + created.TenantID + "/offboarding-plan"
	planResp := e.Do(t, http.MethodPost, planPath, struct{}{}, nil)
	if planResp.Code != http.StatusOK {
		t.Fatalf("offboarding plan = %d %s", planResp.Code, planResp.Body)
	}
	var plan api.PlatformTenantOffboardingPlanResponse
	if err := json.Unmarshal(planResp.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	applyPath := planPath + "/apply"
	req := api.ApplyPlatformTenantOffboardingRequest{ExpectedPlanHash: plan.PlanHash}
	missingKey := e.Do(t, http.MethodPost, applyPath, req, nil)
	if missingKey.Code != http.StatusBadRequest {
		t.Fatalf("apply without idempotency key = %d %s", missingKey.Code, missingKey.Body)
	}
	key := "offboard-" + created.TenantID
	resp := e.Do(t, http.MethodPost, applyPath, req, map[string]string{"Idempotency-Key": key})
	if resp.Code != http.StatusOK {
		t.Fatalf("offboarding apply = %d %s", resp.Code, resp.Body)
	}
	var applied api.PlatformTenantOffboardingApplyResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &applied); err != nil {
		t.Fatal(err)
	}
	if !applied.Applied || applied.TenantID != created.TenantID || applied.PlanHash != plan.PlanHash || applied.ReceiptID == "" || applied.AppliedAt.IsZero() || applied.Actions != plan.Actions {
		t.Fatalf("applied response = %+v, plan = %+v", applied, plan)
	}
	replayed := e.Do(t, http.MethodPost, applyPath, req, map[string]string{"Idempotency-Key": key})
	if replayed.Code != http.StatusOK || replayed.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("idempotent replay = %d %s, replay header=%q", replayed.Code, replayed.Body, replayed.Header().Get("Idempotent-Replayed"))
	}
	var replayedApply api.PlatformTenantOffboardingApplyResponse
	if err := json.Unmarshal(replayed.Body.Bytes(), &replayedApply); err != nil || replayedApply.ReceiptID != applied.ReceiptID {
		t.Fatalf("replayed response = %+v, %v", replayedApply, err)
	}

	tenant, err := (*e.Store()).GetPlatformTenant(ctx, (*e.Acct()).ID, created.TenantID)
	if err != nil || tenant.Status != state.PlatformTenantSuspended {
		t.Fatalf("tenant after apply = %+v, %v", tenant, err)
	}
	consumers, err := (*e.Store()).ListPlatformTenantConsumers(ctx, (*e.Acct()).ID, created.TenantID)
	if err != nil || len(consumers) != 1 || consumers[0].ID != unmanagedConsumer.ID {
		t.Fatalf("retained consumers = %+v, %v", consumers, err)
	}
	surfaces, err := (*e.Store()).ListPlatformTenantSurfaces(ctx, (*e.Acct()).ID, created.TenantID)
	if err != nil || len(surfaces) != 1 || surfaces[0].ID != unmanagedSurfaceID {
		t.Fatalf("retained surfaces = %+v, %v", surfaces, err)
	}
	hostnames, err := (*e.Store()).ListTenantHostnamesForSurface(ctx, created.Surfaces[0].ID)
	if err != nil || len(hostnames) != 1 || hostnames[0].Hostname != "legacy.customer.example" {
		t.Fatalf("retained unmanaged hostname = %+v, %v", hostnames, err)
	}
	consumerKey, err := (*e.Store()).GetConsumerKeyByID(ctx, (*e.Acct()).ID, keyResult.Keys[0].Key.ID)
	if err != nil || consumerKey.RevokedAt == nil {
		t.Fatalf("revoked consumer credential = %+v, %v", consumerKey, err)
	}
	tokens, err := accessStore.ListPlatformTenantAccessTokens(ctx, (*e.Acct()).ID, created.TenantID)
	if err != nil || len(tokens) != 1 || tokens[0].RevokedAt == nil {
		t.Fatalf("revoked access tokens = %+v, %v", tokens, err)
	}
	credentialPolicy, err := state.PlatformTenantCredentialPolicyStore((*e.Store())).GetPlatformTenantCredentialPolicy(ctx, (*e.Acct()).ID, created.TenantID)
	if err != nil || len(credentialPolicy.AllowedScopes) != 0 || credentialPolicy.MaxKeysPerConsumer != 0 {
		t.Fatalf("disabled credential policy = %+v, %v", credentialPolicy, err)
	}
	consumerPolicy, err := state.PlatformTenantConsumerProvisioningPolicyStore((*e.Store())).GetPlatformTenantConsumerProvisioningPolicy(ctx, (*e.Acct()).ID, created.TenantID)
	if err != nil || consumerPolicy.Enabled || consumerPolicy.MaxConsumers != 0 {
		t.Fatalf("disabled consumer policy = %+v, %v", consumerPolicy, err)
	}
	hostnamePolicy, err := state.PlatformTenantHostnamePolicyStore((*e.Store())).GetPlatformTenantHostnamePolicy(ctx, (*e.Acct()).ID, created.TenantID)
	if err != nil || len(hostnamePolicy.AllowedSuffixes) != 0 || hostnamePolicy.MaxHostnames != 0 {
		t.Fatalf("disabled hostname policy = %+v, %v", hostnamePolicy, err)
	}

	listPath := "/v1/account/platform-tenants/" + created.TenantID + "/offboardings"
	listResp := e.Do(t, http.MethodGet, listPath, nil, nil)
	var history api.PlatformTenantOffboardingReceiptListResponse
	if err := json.Unmarshal(listResp.Body.Bytes(), &history); err != nil || listResp.Code != http.StatusOK || len(history.Receipts) != 1 || history.Receipts[0].ReceiptID != applied.ReceiptID {
		t.Fatalf("offboarding history = %d %+v, %v", listResp.Code, history, err)
	}
	receiptResp := e.Do(t, http.MethodGet, listPath+"/"+applied.ReceiptID, nil, nil)
	var receipt api.PlatformTenantOffboardingReceiptResponse
	if err := json.Unmarshal(receiptResp.Body.Bytes(), &receipt); err != nil || receiptResp.Code != http.StatusOK || receipt.ReceiptID != applied.ReceiptID || receipt.Actions != applied.Actions {
		t.Fatalf("offboarding receipt = %d %+v, %v", receiptResp.Code, receipt, err)
	}
}

func TestPlatformTenantOffboardingApplyRejectsStalePlanWithoutChanges(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	tenant, _, err := (*e.Store()).CreatePlatformTenant(context.Background(), (*e.Acct()).ID, "stale-offboard", "Stale offboard", 250)
	if err != nil {
		t.Fatal(err)
	}
	resp := e.Do(t, http.MethodPost, "/v1/account/platform-tenants/"+tenant.ID+"/offboarding-plan", struct{}{}, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("offboarding plan = %d %s", resp.Code, resp.Body)
	}
	var plan api.PlatformTenantOffboardingPlanResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	appID := apidserver.MustSeedApp(t, e, "stale-offboard")
	consumer, err := (*e.Store()).CreateAPIConsumer(context.Background(), (*e.Acct()).ID, appID, "joined-after-plan", "Joined")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (*e.Store()).LinkPlatformTenantConsumer(context.Background(), (*e.Acct()).ID, tenant.ID, consumer.ID); err != nil {
		t.Fatal(err)
	}
	apply := e.Do(t, http.MethodPost, "/v1/account/platform-tenants/"+tenant.ID+"/offboarding-plan/apply",
		api.ApplyPlatformTenantOffboardingRequest{ExpectedPlanHash: plan.PlanHash}, map[string]string{"Idempotency-Key": "stale-offboarding-plan"})
	if apply.Code != http.StatusConflict {
		t.Fatalf("stale apply = %d %s", apply.Code, apply.Body)
	}
	current, err := (*e.Store()).GetPlatformTenant(context.Background(), (*e.Acct()).ID, tenant.ID)
	if err != nil || current.Status != state.PlatformTenantActive {
		t.Fatalf("stale apply changed tenant status: %+v, %v", current, err)
	}
	consumers, err := (*e.Store()).ListPlatformTenantConsumers(context.Background(), (*e.Acct()).ID, tenant.ID)
	if err != nil || len(consumers) != 1 || consumers[0].ID != consumer.ID {
		t.Fatalf("stale apply changed links: %+v, %v", consumers, err)
	}
}
