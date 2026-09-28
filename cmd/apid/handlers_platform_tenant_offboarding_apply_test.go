package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPlatformTenantOffboardingApplyIsAtomicAndRecoverable(t *testing.T) {
	withTenantSurfacesEnabled(t)
	e := setup(t, api.PlanPro)
	appID := mustSeedApp(t, e, "offboarding-apply")
	created := readApplyResponse(t, e.do(t, http.MethodPost, "/v1/account/platform-tenants/apply", api.ApplyPlatformTenantRequest{
		ExternalRef: "offboard-apply", Name: "Offboard apply",
		Consumers: []api.ApplyPlatformTenantConsumerRequest{{AppID: appID, ExternalRef: "managed", Name: "Managed"}},
		Surfaces: []api.ApplyPlatformTenantSurfaceRequest{{AppID: appID, Name: "Managed surface",
			Hostnames: []string{"managed.customer.example", "second.customer.example"}}},
	}, nil))
	if len(created.Consumers) != 1 || len(created.Surfaces) != 1 {
		t.Fatalf("initial tenant apply = %+v", created)
	}

	ctx := context.Background()
	unmanagedConsumer, err := e.store.CreateAPIConsumer(ctx, e.acct.ID, appID, "unmanaged", "Unmanaged")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.LinkPlatformTenantConsumer(ctx, e.acct.ID, created.TenantID, unmanagedConsumer.ID); err != nil {
		t.Fatal(err)
	}
	unmanagedSurfaceID := seedTenantSurface(t, e, appID, "Unmanaged surface")
	if _, err := e.store.LinkPlatformTenantSurface(ctx, e.acct.ID, created.TenantID, unmanagedSurfaceID); err != nil {
		t.Fatal(err)
	}
	limits, ok := api.LimitsFor(e.acct.Plan)
	if !ok {
		t.Fatal("missing account plan limits")
	}
	if _, err := e.store.CreateTenantHostnameIfUnderQuota(ctx, state.CreateTenantHostnameParams{
		SurfaceID: created.Surfaces[0].ID, Hostname: "legacy.customer.example", ChallengeToken: "legacy-challenge",
	}, limits); err != nil {
		t.Fatal(err)
	}
	_, keyPrefix, keyHash, err := api.GenerateConsumerKey()
	if err != nil {
		t.Fatal(err)
	}
	credentialStore := state.PlatformTenantCredentialStore(e.store)
	keyResult, err := credentialStore.ApplyPlatformTenantCredentials(ctx, state.ApplyPlatformTenantCredentialsParams{
		AccountID: e.acct.ID, TenantID: created.TenantID, AppLimit: 100, AccountLimit: 100,
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
	accessStore := state.PlatformTenantAccessStore(e.store)
	if _, err := accessStore.CreatePlatformTenantAccessToken(ctx, state.PlatformTenantAccessTokenInput{
		AccountID: e.acct.ID, TenantID: created.TenantID, Name: "customer automation", Prefix: tokenPrefix,
		TokenHash: tokenHash, Scopes: []string{api.ScopePlatformTenantConsumersManage}, ExpiresAt: time.Now().UTC().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := state.PlatformTenantCredentialPolicyStore(e.store).SetPlatformTenantCredentialPolicy(ctx, e.acct.ID, created.TenantID, []string{"read"}, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := state.PlatformTenantConsumerProvisioningPolicyStore(e.store).SetPlatformTenantConsumerProvisioningPolicy(ctx, e.acct.ID, created.TenantID, true, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := state.PlatformTenantHostnamePolicyStore(e.store).SetPlatformTenantHostnamePolicy(ctx, e.acct.ID, created.TenantID, []string{"customer.example"}, 5); err != nil {
		t.Fatal(err)
	}

	planPath := "/v1/account/platform-tenants/" + created.TenantID + "/offboarding-plan"
	planResp := e.do(t, http.MethodPost, planPath, struct{}{}, nil)
	if planResp.Code != http.StatusOK {
		t.Fatalf("offboarding plan = %d %s", planResp.Code, planResp.Body)
	}
	var plan api.PlatformTenantOffboardingPlanResponse
	if err := json.Unmarshal(planResp.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	applyPath := planPath + "/apply"
	req := api.ApplyPlatformTenantOffboardingRequest{ExpectedPlanHash: plan.PlanHash}
	missingKey := e.do(t, http.MethodPost, applyPath, req, nil)
	if missingKey.Code != http.StatusBadRequest {
		t.Fatalf("apply without idempotency key = %d %s", missingKey.Code, missingKey.Body)
	}
	key := "offboard-" + created.TenantID
	resp := e.do(t, http.MethodPost, applyPath, req, map[string]string{"Idempotency-Key": key})
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
	replayed := e.do(t, http.MethodPost, applyPath, req, map[string]string{"Idempotency-Key": key})
	if replayed.Code != http.StatusOK || replayed.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("idempotent replay = %d %s, replay header=%q", replayed.Code, replayed.Body, replayed.Header().Get("Idempotent-Replayed"))
	}
	var replayedApply api.PlatformTenantOffboardingApplyResponse
	if err := json.Unmarshal(replayed.Body.Bytes(), &replayedApply); err != nil || replayedApply.ReceiptID != applied.ReceiptID {
		t.Fatalf("replayed response = %+v, %v", replayedApply, err)
	}

	tenant, err := e.store.GetPlatformTenant(ctx, e.acct.ID, created.TenantID)
	if err != nil || tenant.Status != state.PlatformTenantSuspended {
		t.Fatalf("tenant after apply = %+v, %v", tenant, err)
	}
	consumers, err := e.store.ListPlatformTenantConsumers(ctx, e.acct.ID, created.TenantID)
	if err != nil || len(consumers) != 1 || consumers[0].ID != unmanagedConsumer.ID {
		t.Fatalf("retained consumers = %+v, %v", consumers, err)
	}
	surfaces, err := e.store.ListPlatformTenantSurfaces(ctx, e.acct.ID, created.TenantID)
	if err != nil || len(surfaces) != 1 || surfaces[0].ID != unmanagedSurfaceID {
		t.Fatalf("retained surfaces = %+v, %v", surfaces, err)
	}
	hostnames, err := e.store.ListTenantHostnamesForSurface(ctx, created.Surfaces[0].ID)
	if err != nil || len(hostnames) != 1 || hostnames[0].Hostname != "legacy.customer.example" {
		t.Fatalf("retained unmanaged hostname = %+v, %v", hostnames, err)
	}
	consumerKey, err := e.store.GetConsumerKeyByID(ctx, e.acct.ID, keyResult.Keys[0].Key.ID)
	if err != nil || consumerKey.RevokedAt == nil {
		t.Fatalf("revoked consumer credential = %+v, %v", consumerKey, err)
	}
	tokens, err := accessStore.ListPlatformTenantAccessTokens(ctx, e.acct.ID, created.TenantID)
	if err != nil || len(tokens) != 1 || tokens[0].RevokedAt == nil {
		t.Fatalf("revoked access tokens = %+v, %v", tokens, err)
	}
	credentialPolicy, err := state.PlatformTenantCredentialPolicyStore(e.store).GetPlatformTenantCredentialPolicy(ctx, e.acct.ID, created.TenantID)
	if err != nil || len(credentialPolicy.AllowedScopes) != 0 || credentialPolicy.MaxKeysPerConsumer != 0 {
		t.Fatalf("disabled credential policy = %+v, %v", credentialPolicy, err)
	}
	consumerPolicy, err := state.PlatformTenantConsumerProvisioningPolicyStore(e.store).GetPlatformTenantConsumerProvisioningPolicy(ctx, e.acct.ID, created.TenantID)
	if err != nil || consumerPolicy.Enabled || consumerPolicy.MaxConsumers != 0 {
		t.Fatalf("disabled consumer policy = %+v, %v", consumerPolicy, err)
	}
	hostnamePolicy, err := state.PlatformTenantHostnamePolicyStore(e.store).GetPlatformTenantHostnamePolicy(ctx, e.acct.ID, created.TenantID)
	if err != nil || len(hostnamePolicy.AllowedSuffixes) != 0 || hostnamePolicy.MaxHostnames != 0 {
		t.Fatalf("disabled hostname policy = %+v, %v", hostnamePolicy, err)
	}

	listPath := "/v1/account/platform-tenants/" + created.TenantID + "/offboardings"
	listResp := e.do(t, http.MethodGet, listPath, nil, nil)
	var history api.PlatformTenantOffboardingReceiptListResponse
	if err := json.Unmarshal(listResp.Body.Bytes(), &history); err != nil || listResp.Code != http.StatusOK || len(history.Receipts) != 1 || history.Receipts[0].ReceiptID != applied.ReceiptID {
		t.Fatalf("offboarding history = %d %+v, %v", listResp.Code, history, err)
	}
	receiptResp := e.do(t, http.MethodGet, listPath+"/"+applied.ReceiptID, nil, nil)
	var receipt api.PlatformTenantOffboardingReceiptResponse
	if err := json.Unmarshal(receiptResp.Body.Bytes(), &receipt); err != nil || receiptResp.Code != http.StatusOK || receipt.ReceiptID != applied.ReceiptID || receipt.Actions != applied.Actions {
		t.Fatalf("offboarding receipt = %d %+v, %v", receiptResp.Code, receipt, err)
	}
}

func TestPlatformTenantOffboardingApplyRejectsStalePlanWithoutChanges(t *testing.T) {
	e := setup(t, api.PlanPro)
	tenant, _, err := e.store.CreatePlatformTenant(context.Background(), e.acct.ID, "stale-offboard", "Stale offboard", 250)
	if err != nil {
		t.Fatal(err)
	}
	resp := e.do(t, http.MethodPost, "/v1/account/platform-tenants/"+tenant.ID+"/offboarding-plan", struct{}{}, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("offboarding plan = %d %s", resp.Code, resp.Body)
	}
	var plan api.PlatformTenantOffboardingPlanResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	appID := mustSeedApp(t, e, "stale-offboard")
	consumer, err := e.store.CreateAPIConsumer(context.Background(), e.acct.ID, appID, "joined-after-plan", "Joined")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.LinkPlatformTenantConsumer(context.Background(), e.acct.ID, tenant.ID, consumer.ID); err != nil {
		t.Fatal(err)
	}
	apply := e.do(t, http.MethodPost, "/v1/account/platform-tenants/"+tenant.ID+"/offboarding-plan/apply",
		api.ApplyPlatformTenantOffboardingRequest{ExpectedPlanHash: plan.PlanHash}, map[string]string{"Idempotency-Key": "stale-offboarding-plan"})
	if apply.Code != http.StatusConflict {
		t.Fatalf("stale apply = %d %s", apply.Code, apply.Body)
	}
	current, err := e.store.GetPlatformTenant(context.Background(), e.acct.ID, tenant.ID)
	if err != nil || current.Status != state.PlatformTenantActive {
		t.Fatalf("stale apply changed tenant status: %+v, %v", current, err)
	}
	consumers, err := e.store.ListPlatformTenantConsumers(context.Background(), e.acct.ID, tenant.ID)
	if err != nil || len(consumers) != 1 || consumers[0].ID != consumer.ID {
		t.Fatalf("stale apply changed links: %+v, %v", consumers, err)
	}
}
