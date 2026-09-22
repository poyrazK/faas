package state

// ADR-212 state-layer coverage for endpoint quotas and soft-delete isolation.

import (
	"bytes"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemStoreInboundWebhookQuotaAndTokenLookup(t *testing.T) {
	store := NewMemStore()
	account, err := store.CreateAccount(t.Context(), "inbound@example.test", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := store.CreateApp(t.Context(), App{AccountID: account.ID, Slug: "inbound-state", Type: AppTypeApp, Status: AppActive})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	limits := api.MustLimitsFor(api.PlanHobby)
	limits.InboundWebhookPerApp = 1
	limits.InboundWebhookPerAccount = 1
	tokenHash := bytes.Repeat([]byte{0x42}, 32)
	created, err := store.CreateInboundWebhookEndpointIfUnderQuota(t.Context(), InboundWebhookEndpoint{
		AppID: app.ID, AccountID: account.ID, Name: "stripe", Provider: InboundWebhookProviderStripe,
		TokenHash: tokenHash, SigningSecretSealed: []byte("sealed"), DeliveryPath: "/stripe", Enabled: true,
	}, limits)
	if err != nil {
		t.Fatalf("CreateInboundWebhookEndpointIfUnderQuota: %v", err)
	}
	got, err := store.InboundWebhookEndpointByTokenHash(t.Context(), tokenHash)
	if err != nil || got.ID != created.ID {
		t.Fatalf("token lookup = %#v, %v", got, err)
	}

	_, err = store.CreateInboundWebhookEndpointIfUnderQuota(t.Context(), InboundWebhookEndpoint{
		AppID: app.ID, AccountID: account.ID, Name: "second", Provider: InboundWebhookProviderStripe,
		TokenHash: bytes.Repeat([]byte{0x24}, 32), SigningSecretSealed: []byte("sealed"), Enabled: true,
	}, limits)
	var quota *InboundWebhookQuotaError
	if !errors.As(err, &quota) || quota.Scope != InboundWebhookQuotaScopeApp {
		t.Fatalf("second create error = %v, want app quota", err)
	}

	if _, err := store.SoftDeleteAppCascade(t.Context(), app.ID); err != nil {
		t.Fatalf("SoftDeleteAppCascade: %v", err)
	}
	if _, err := store.InboundWebhookEndpointByTokenHash(t.Context(), tokenHash); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted app token lookup error = %v, want ErrNotFound", err)
	}
}
