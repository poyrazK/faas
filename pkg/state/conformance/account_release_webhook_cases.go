package conformance

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Both stores must reject invalid account filters, serialize the same quota,
// and expose one account subscription's deliveries across source apps without
// exposing them to a different account.
func testAccountReleaseWebhookQuotaAndPagination(t *testing.T, fx *Fixture) {
	input := state.AppWebhook{
		AccountID: fx.Account.ID, Scope: state.AppWebhookScopeAccount,
		TargetURL:    "https://example.com/releases-" + uuid.NewString(),
		SecretSealed: []byte("sealed"), EventFilter: []string{"deployment.live"},
		Enabled: true,
	}
	limits := api.Limits{WebhookPerAccount: 1}
	invalid := input
	invalid.EventFilter = nil
	if _, err := fx.Store.CreateAccountReleaseWebhookIfUnderQuota(fx.Ctx, invalid, limits); !errors.Is(err, state.ErrInvalidAppWebhookScope) {
		t.Fatalf("empty account filter = %v, want ErrInvalidAppWebhookScope", err)
	}
	hook, err := fx.Store.CreateAccountReleaseWebhookIfUnderQuota(fx.Ctx, input, limits)
	if err != nil {
		t.Fatalf("create account hook: %v", err)
	}
	if hook.AppID != "" || hook.Scope != state.AppWebhookScopeAccount || hook.AccountID != fx.Account.ID {
		t.Fatalf("created hook scope/owner = %+v", hook)
	}
	second := input
	second.TargetURL = "https://example.com/releases-" + uuid.NewString()
	_, err = fx.Store.CreateAccountReleaseWebhookIfUnderQuota(fx.Ctx, second, limits)
	var quota *state.AppWebhookQuotaError
	if !errors.As(err, &quota) || quota.Scope != state.AppWebhookQuotaScopeAccount || quota.Observed != 1 {
		t.Fatalf("second account hook = %v, want account quota with observed=1", err)
	}

	newApp, err := fx.Store.CreateApp(fx.Ctx, state.App{
		AccountID: fx.Account.ID, Slug: "conformance-account-hook-" + uuid.NewString(),
		Type: state.AppTypeApp, Runtime: "node22", RAMMB: 128,
		MaxConcurrency: 1, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatalf("create future app: %v", err)
	}
	for _, appID := range []string{fx.App.ID, newApp.ID} {
		if _, err := fx.Store.RecordAppWebhookDelivery(fx.Ctx, state.AppWebhookDelivery{
			WebhookID: hook.ID, AppID: appID, AccountID: fx.Account.ID,
			Event: state.AppWebhookEventDeploymentLive, Payload: []byte(`{"v":1}`),
			Status: state.AppWebhookDeliveryPending,
		}); err != nil {
			t.Fatalf("record delivery for %s: %v", appID, err)
		}
	}
	first, next, err := fx.Store.ListAccountReleaseWebhookDeliveries(fx.Ctx, fx.Account.ID, hook.ID, 1, "")
	if err != nil || len(first) != 1 || next == "" {
		t.Fatalf("first page len=%d next=%q err=%v", len(first), next, err)
	}
	secondPage, end, err := fx.Store.ListAccountReleaseWebhookDeliveries(fx.Ctx, fx.Account.ID, hook.ID, 1, next)
	if err != nil || len(secondPage) != 1 || end != "" || secondPage[0].ID == first[0].ID {
		t.Fatalf("second page len=%d end=%q err=%v", len(secondPage), end, err)
	}
	sources := map[string]bool{first[0].AppID: true, secondPage[0].AppID: true}
	if !sources[fx.App.ID] || !sources[newApp.ID] {
		t.Fatalf("cross-app sources = %v", sources)
	}
	foreign, _, err := fx.Store.ListAccountReleaseWebhookDeliveries(fx.Ctx, uuid.NewString(), hook.ID, 10, "")
	if err != nil || len(foreign) != 0 {
		t.Fatalf("foreign page len=%d err=%v", len(foreign), err)
	}
}
