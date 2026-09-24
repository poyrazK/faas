package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func pgAccountReleaseHook(accountID string) state.AppWebhook {
	return state.AppWebhook{
		AccountID: accountID, Scope: state.AppWebhookScopeAccount,
		TargetURL:    "https://example.com/release-" + uuid.NewString(),
		SecretSealed: []byte("sealed-secret"), EventFilter: []string{"deployment.live"},
		Enabled: true,
	}
}

// Both create paths acquire the same account lock and count both scopes.
func TestPgStore_AccountReleaseWebhook_SharedQuotaConcurrent(t *testing.T) {
	s, ctx := pgStore(t)
	accountID, appID, _ := seedLiveDeploy(t, s, ctx, "account-release-concurrent")
	limits := api.Limits{WebhookPerApp: 2, WebhookPerAccount: 1}
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() {
		<-start
		_, err := s.CreateAccountReleaseWebhookIfUnderQuota(ctx, pgAccountReleaseHook(accountID), limits)
		results <- err
	}()
	go func() {
		<-start
		_, err := s.CreateAppWebhookIfUnderQuota(ctx, pgSampleWebhook(accountID, appID), limits)
		results <- err
	}()
	close(start)
	successes, quotas := 0, 0
	for range 2 {
		err := <-results
		if err == nil {
			successes++
			continue
		}
		var quota *state.AppWebhookQuotaError
		if errors.As(err, &quota) && quota.Scope == state.AppWebhookQuotaScopeAccount {
			quotas++
			continue
		}
		t.Fatalf("concurrent create: %v", err)
	}
	if successes != 1 || quotas != 1 {
		t.Fatalf("successes=%d quotas=%d, want 1/1", successes, quotas)
	}
}

func TestPgStore_AccountReleaseWebhook_DeliveriesAcrossApps(t *testing.T) {
	s, ctx := pgStore(t)
	accountID, firstAppID, _ := seedLiveDeploy(t, s, ctx, "account-release-list")
	second, err := s.CreateApp(ctx, state.App{
		AccountID: accountID, Slug: "account-release-second-" + uuid.NewString(),
		Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 1, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	hook, err := s.CreateAccountReleaseWebhookIfUnderQuota(ctx, pgAccountReleaseHook(accountID), api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatal(err)
	}
	if hook.Scope != state.AppWebhookScopeAccount || hook.AppID != "" {
		t.Fatalf("created scope/app = %q/%q", hook.Scope, hook.AppID)
	}
	for _, appID := range []string{firstAppID, second.ID} {
		_, err := s.RecordAppWebhookDelivery(ctx, state.AppWebhookDelivery{
			WebhookID: hook.ID, AppID: appID, AccountID: accountID,
			Event: state.AppWebhookEventDeploymentLive, Payload: []byte(`{"app_id":"` + appID + `"}`),
			Status: state.AppWebhookDeliveryPending, NextAttemptAt: time.Now(),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	page, next, err := s.ListAccountReleaseWebhookDeliveries(ctx, accountID, hook.ID, 10, "")
	if err != nil || next != "" || len(page) != 2 {
		t.Fatalf("deliveries=%d next=%q err=%v", len(page), next, err)
	}
	seen := map[string]bool{}
	for _, delivery := range page {
		seen[delivery.AppID] = true
	}
	if !seen[firstAppID] || !seen[second.ID] {
		t.Fatalf("sources: %v", seen)
	}
	page, _, err = s.ListAccountReleaseWebhookDeliveries(ctx, "00000000-0000-0000-0000-000000000000", hook.ID, 10, "")
	if err != nil || len(page) != 0 {
		t.Fatalf("foreign account deliveries=%d err=%v", len(page), err)
	}
}
