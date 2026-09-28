package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestValidPlatformTenantWebhookFilter(t *testing.T) {
	tests := []struct {
		name   string
		events []string
		valid  bool
	}{
		{name: "statement default", events: []string{state.PlatformTenantStatementFinalizedEvent}, valid: true},
		{name: "hostname verified", events: []string{state.PlatformTenantHostnameVerifiedEvent}, valid: true},
		{name: "both events", events: []string{state.PlatformTenantStatementFinalizedEvent, state.PlatformTenantHostnameVerifiedEvent}, valid: true},
		{name: "empty", events: []string{}, valid: false},
		{name: "unknown", events: []string{"platform_tenant.hostname.failed"}, valid: false},
		{name: "duplicate", events: []string{state.PlatformTenantHostnameVerifiedEvent, state.PlatformTenantHostnameVerifiedEvent}, valid: false},
		{name: "too many", events: []string{state.PlatformTenantStatementFinalizedEvent, state.PlatformTenantHostnameVerifiedEvent, "platform_tenant.hostname.failed"}, valid: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := state.ValidPlatformTenantWebhookFilter(tt.events); got != tt.valid {
				t.Fatalf("ValidPlatformTenantWebhookFilter(%v) = %v, want %v", tt.events, got, tt.valid)
			}
		})
	}
	if state.ValidAppWebhookEvent(state.AppWebhookEvent(state.PlatformTenantHostnameVerifiedEvent)) {
		t.Fatal("tenant-owned hostname event must not enter the app webhook vocabulary")
	}
}

func TestMemPlatformTenantWebhookEventFilterIsImmutable(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "tenant-webhook-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.CreatePlatformTenant(ctx, account.ID, "customer", "Customer", 10)
	if err != nil {
		t.Fatal(err)
	}
	hook, err := store.CreatePlatformTenantWebhookIfUnderQuota(ctx, state.AppWebhook{
		AccountID: account.ID, PlatformTenantID: tenant.ID, Scope: state.AppWebhookScopePlatformTenant,
		TargetURL: "https://example.test/events", SecretSealed: []byte("sealed"),
		EventFilter: []string{state.PlatformTenantStatementFinalizedEvent}, Enabled: true,
	}, api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatal(err)
	}
	filter := []string{state.PlatformTenantHostnameVerifiedEvent}
	if _, err := store.UpdateAppWebhook(ctx, hook.ID, state.UpdateAppWebhookParams{EventFilter: &filter}); !errors.Is(err, state.ErrInvalidAppWebhookScope) {
		t.Fatalf("tenant webhook filter update error = %v, want immutable-filter rejection", err)
	}
}

func TestPgPlatformTenantHostnameVerificationWebhook(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	tenant, _, err := store.CreatePlatformTenant(ctx, accountID, "host-customer-"+uuid.NewString()[:8], "Host customer", 250)
	if err != nil {
		t.Fatal(err)
	}
	limits := api.MustLimitsFor(api.PlanPro)
	surface, err := store.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{
		AccountID: accountID, AppID: appID, Name: "customer-hosts",
	}, limits)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.LinkPlatformTenantSurface(ctx, accountID, tenant.ID, surface.ID); err != nil {
		t.Fatal(err)
	}
	hook, err := store.CreatePlatformTenantWebhookIfUnderQuota(ctx, state.AppWebhook{
		AccountID: accountID, PlatformTenantID: tenant.ID, Scope: state.AppWebhookScopePlatformTenant,
		TargetURL: "https://example.com/tenant-events", SecretSealed: []byte("sealed"),
		EventFilter: []string{state.PlatformTenantHostnameVerifiedEvent}, Enabled: true,
	}, limits)
	if err != nil {
		t.Fatal(err)
	}
	host, err := store.CreateTenantHostnameIfUnderQuota(ctx, state.CreateTenantHostnameParams{
		SurfaceID: surface.ID, Hostname: "shop.customer.example", ChallengeToken: "must-not-appear-in-webhook",
	}, limits)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkTenantHostnameVerified(ctx, host.Hostname); err != nil {
		t.Fatal(err)
	}
	// A verified hostname on an unlinked surface must not enter this tenant's
	// event stream, even when it belongs to the same account and app.
	unlinkedSurface, err := store.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{
		AccountID: accountID, AppID: appID, Name: "unlinked-customer-hosts",
	}, limits)
	if err != nil {
		t.Fatal(err)
	}
	unlinkedHost, err := store.CreateTenantHostnameIfUnderQuota(ctx, state.CreateTenantHostnameParams{
		SurfaceID: unlinkedSurface.ID, Hostname: "other.customer.example", ChallengeToken: "unlinked-secret",
	}, limits)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkTenantHostnameVerified(ctx, unlinkedHost.Hostname); err != nil {
		t.Fatal(err)
	}
	// A repeated verification is not a new lifecycle transition.
	if err := store.MarkTenantHostnameVerified(ctx, host.Hostname); err != nil {
		t.Fatal(err)
	}
	deliveries, next, err := store.ListPlatformTenantWebhookDeliveries(ctx, accountID, tenant.ID, hook.ID, 50, "")
	if err != nil {
		t.Fatal(err)
	}
	if next != "" || len(deliveries) != 1 {
		t.Fatalf("deliveries = %d, next = %q; want one durable delivery", len(deliveries), next)
	}
	if deliveries[0].Event != state.PlatformTenantHostnameVerifiedEvent {
		t.Fatalf("event = %q", deliveries[0].Event)
	}
	var payload api.PlatformTenantHostnameVerifiedWebhookPayload
	if err := json.Unmarshal(deliveries[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.PlatformTenantID != tenant.ID || payload.ExternalRef != tenant.ExternalRef ||
		payload.SurfaceID != surface.ID || payload.SurfaceName != surface.Name || payload.AppID != appID ||
		payload.HostnameID != host.ID || payload.Hostname != host.Hostname || payload.VerifiedAt.IsZero() {
		t.Fatalf("payload = %+v", payload)
	}
	if string(deliveries[0].Payload) == "" || containsJSONStringKey(deliveries[0].Payload, "challenge_token") {
		t.Fatalf("payload is empty or includes the DNS challenge token: %s", deliveries[0].Payload)
	}
}

func containsJSONStringKey(body json.RawMessage, key string) bool {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(body, &object); err != nil {
		return false
	}
	_, ok := object[key]
	return ok
}
