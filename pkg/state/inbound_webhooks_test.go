package state_test

// ADR-212 state-layer coverage for endpoint quotas and soft-delete isolation.

import (
	"bytes"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemStoreInboundWebhookQuotaAndTokenLookup(t *testing.T) {
	store := state.NewMemStore()
	account, err := store.CreateAccount(t.Context(), "inbound@example.test", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "inbound-state", Type: state.AppTypeApp, Status: state.AppActive})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	limits := api.MustLimitsFor(api.PlanHobby)
	limits.InboundWebhookPerApp = 1
	limits.InboundWebhookPerAccount = 1
	tokenHash := bytes.Repeat([]byte{0x42}, 32)
	created, err := store.CreateInboundWebhookEndpointIfUnderQuota(t.Context(), state.InboundWebhookEndpoint{
		AppID: app.ID, AccountID: account.ID, Name: "stripe", Provider: state.InboundWebhookProviderStripe,
		TokenHash: tokenHash, SigningSecretSealed: []byte("sealed"), DeliveryPath: "/stripe", Enabled: true,
	}, limits)
	if err != nil {
		t.Fatalf("CreateInboundWebhookEndpointIfUnderQuota: %v", err)
	}
	got, err := store.InboundWebhookEndpointByTokenHash(t.Context(), tokenHash)
	if err != nil || got.ID != created.ID {
		t.Fatalf("token lookup = %#v, %v", got, err)
	}

	_, err = store.CreateInboundWebhookEndpointIfUnderQuota(t.Context(), state.InboundWebhookEndpoint{
		AppID: app.ID, AccountID: account.ID, Name: "second", Provider: state.InboundWebhookProviderStripe,
		TokenHash: bytes.Repeat([]byte{0x24}, 32), SigningSecretSealed: []byte("sealed"), Enabled: true,
	}, limits)
	var quota *state.InboundWebhookQuotaError
	if !errors.As(err, &quota) || quota.Scope != state.InboundWebhookQuotaScopeApp {
		t.Fatalf("second create error = %v, want app quota", err)
	}

	if _, err := store.SoftDeleteAppCascade(t.Context(), app.ID); err != nil {
		t.Fatalf("SoftDeleteAppCascade: %v", err)
	}
	if _, err := store.InboundWebhookEndpointByTokenHash(t.Context(), tokenHash); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("deleted app token lookup error = %v, want ErrNotFound", err)
	}
}
