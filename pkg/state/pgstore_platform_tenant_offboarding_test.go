package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgPlatformTenantOffboardingApplyRevokesAndPersistsReceipt(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	externalRef := "offboard-" + uuid.NewString()
	created, err := store.ApplyPlatformTenant(ctx, state.ApplyPlatformTenantParams{
		AccountID: accountID, ExternalRef: externalRef, Name: "Offboard Customer", TenantLimit: 250,
		Limits:    api.MustLimitsFor(api.PlanPro),
		Consumers: []state.ApplyPlatformTenantConsumer{{AppID: appID, ExternalRef: "managed", Name: "Managed"}},
	})
	if err != nil || created.Tenant.ID == "" || len(created.Consumers) != 1 {
		t.Fatalf("initial tenant apply = %+v, %v", created, err)
	}
	_, prefix, hash, err := api.GenerateConsumerKey()
	if err != nil {
		t.Fatal(err)
	}
	keyResult, err := store.ApplyPlatformTenantCredentials(ctx, state.ApplyPlatformTenantCredentialsParams{
		AccountID: accountID, TenantID: created.Tenant.ID, AppLimit: 100, AccountLimit: 100,
		Keys: []state.PlatformTenantCredentialIntent{{ConsumerID: created.Consumers[0].Consumer.ID, Name: "customer-key",
			Prefix: prefix, Hash: hash, Scopes: []string{"read"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, tokenPrefix, tokenHash, err := api.GeneratePlatformTenantAccessToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreatePlatformTenantAccessToken(ctx, state.PlatformTenantAccessTokenInput{
		AccountID: accountID, TenantID: created.Tenant.ID, Name: "automation", Prefix: tokenPrefix, TokenHash: tokenHash,
		Scopes: []string{api.ScopePlatformTenantConsumersManage}, ExpiresAt: time.Now().UTC().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetPlatformTenantCredentialPolicy(ctx, accountID, created.Tenant.ID, []string{"read"}, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetPlatformTenantConsumerProvisioningPolicy(ctx, accountID, created.Tenant.ID, true, 4); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetPlatformTenantHostnamePolicy(ctx, accountID, created.Tenant.ID, []string{"customer.example"}, 4); err != nil {
		t.Fatal(err)
	}

	plan, err := store.PlanPlatformTenantOffboarding(ctx, accountID, created.Tenant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Actions.SuspendTenant || plan.Actions.RevokeConsumerKeys != 1 || plan.Actions.RevokeAccessTokens != 1 ||
		plan.Actions.DetachManagedConsumers != 1 || !plan.Actions.DisableCredentialDelegation ||
		!plan.Actions.DisableCustomerProvisioning || !plan.Actions.DisableHostnameDelegation {
		t.Fatalf("offboarding plan = %+v", plan)
	}
	applied, err := store.ApplyPlatformTenantOffboarding(ctx, accountID, created.Tenant.ID, plan.PlanHash)
	if err != nil || !applied.Applied || applied.ReceiptID == "" || applied.PlanHash != plan.PlanHash || applied.Actions != plan.Actions {
		t.Fatalf("offboarding apply = %+v, %v", applied, err)
	}
	tenant, err := store.GetPlatformTenant(ctx, accountID, created.Tenant.ID)
	if err != nil || tenant.Status != state.PlatformTenantSuspended {
		t.Fatalf("tenant after offboarding = %+v, %v", tenant, err)
	}
	consumers, err := store.ListPlatformTenantConsumers(ctx, accountID, created.Tenant.ID)
	if err != nil || len(consumers) != 0 {
		t.Fatalf("managed consumers after offboarding = %+v, %v", consumers, err)
	}
	key, err := store.GetConsumerKeyByID(ctx, accountID, keyResult.Keys[0].Key.ID)
	if err != nil || key.RevokedAt == nil {
		t.Fatalf("consumer key after offboarding = %+v, %v", key, err)
	}
	tokens, err := store.ListPlatformTenantAccessTokens(ctx, accountID, created.Tenant.ID)
	if err != nil || len(tokens) != 1 || tokens[0].RevokedAt == nil {
		t.Fatalf("access tokens after offboarding = %+v, %v", tokens, err)
	}
	credentialPolicy, err := store.GetPlatformTenantCredentialPolicy(ctx, accountID, created.Tenant.ID)
	if err != nil || len(credentialPolicy.AllowedScopes) != 0 || credentialPolicy.MaxKeysPerConsumer != 0 {
		t.Fatalf("credential policy after offboarding = %+v, %v", credentialPolicy, err)
	}
	consumerPolicy, err := store.GetPlatformTenantConsumerProvisioningPolicy(ctx, accountID, created.Tenant.ID)
	if err != nil || consumerPolicy.Enabled || consumerPolicy.MaxConsumers != 0 {
		t.Fatalf("consumer policy after offboarding = %+v, %v", consumerPolicy, err)
	}
	hostnamePolicy, err := store.GetPlatformTenantHostnamePolicy(ctx, accountID, created.Tenant.ID)
	if err != nil || len(hostnamePolicy.AllowedSuffixes) != 0 || hostnamePolicy.MaxHostnames != 0 {
		t.Fatalf("hostname policy after offboarding = %+v, %v", hostnamePolicy, err)
	}
	receipts, nextToken, err := store.ListPlatformTenantOffboardingReceipts(ctx, accountID, created.Tenant.ID, 10, "")
	if err != nil || len(receipts) != 1 || nextToken != "" || receipts[0].ReceiptID != applied.ReceiptID {
		t.Fatalf("offboarding receipts = %+v, next=%q, %v", receipts, nextToken, err)
	}
	receipt, err := store.GetPlatformTenantOffboardingReceipt(ctx, accountID, created.Tenant.ID, applied.ReceiptID)
	if err != nil || receipt.PlanHash != plan.PlanHash || receipt.Actions != applied.Actions {
		t.Fatalf("offboarding receipt = %+v, %v", receipt, err)
	}
}

func TestPgPlatformTenantOffboardingApplyRejectsStalePlan(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	tenant, _, err := store.CreatePlatformTenant(ctx, accountID, "stale-offboard-"+uuid.NewString(), "Stale", 250)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := store.PlanPlatformTenantOffboarding(ctx, accountID, tenant.ID)
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := store.CreateAPIConsumer(ctx, accountID, appID, "joined-"+uuid.NewString(), "Joined")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.LinkPlatformTenantConsumer(ctx, accountID, tenant.ID, consumer.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyPlatformTenantOffboarding(ctx, accountID, tenant.ID, plan.PlanHash); !errors.Is(err, state.ErrPlatformTenantPlanStale) {
		t.Fatalf("stale offboarding apply = %v", err)
	}
	current, err := store.GetPlatformTenant(ctx, accountID, tenant.ID)
	if err != nil || current.Status != state.PlatformTenantActive {
		t.Fatalf("stale apply changed tenant status: %+v, %v", current, err)
	}
	consumers, err := store.ListPlatformTenantConsumers(ctx, accountID, tenant.ID)
	if err != nil || len(consumers) != 1 || consumers[0].ID != consumer.ID {
		t.Fatalf("stale apply changed links: %+v, %v", consumers, err)
	}
}
