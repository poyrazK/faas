package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

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
		{name: "certificate changed", events: []string{state.PlatformTenantSurfaceCertificateChangedEvent}, valid: true},
		{name: "deployment changed", events: []string{state.PlatformTenantSurfaceDeploymentChangedEvent}, valid: true},
		{name: "customer linked", events: []string{state.PlatformTenantCustomerLinkedEvent}, valid: true},
		{name: "customer offboarded", events: []string{state.PlatformTenantCustomerOffboardedEvent}, valid: true},
		{name: "reconciliation applied", events: []string{state.PlatformTenantReconciliationAppliedEvent}, valid: true},
		{name: "both events", events: []string{state.PlatformTenantStatementFinalizedEvent, state.PlatformTenantHostnameVerifiedEvent}, valid: true},
		{name: "all events", events: []string{state.PlatformTenantStatementFinalizedEvent, state.PlatformTenantHostnameVerifiedEvent, state.PlatformTenantSurfaceCertificateChangedEvent}, valid: true},
		{name: "all four events", events: []string{state.PlatformTenantStatementFinalizedEvent, state.PlatformTenantHostnameVerifiedEvent, state.PlatformTenantSurfaceCertificateChangedEvent, state.PlatformTenantSurfaceDeploymentChangedEvent}, valid: true},
		{name: "all six existing events", events: []string{state.PlatformTenantStatementFinalizedEvent, state.PlatformTenantHostnameVerifiedEvent,
			state.PlatformTenantSurfaceCertificateChangedEvent, state.PlatformTenantSurfaceDeploymentChangedEvent,
			state.PlatformTenantCustomerLinkedEvent, state.PlatformTenantCustomerOffboardedEvent}, valid: true},
		{name: "all seven events", events: []string{state.PlatformTenantStatementFinalizedEvent, state.PlatformTenantHostnameVerifiedEvent,
			state.PlatformTenantSurfaceCertificateChangedEvent, state.PlatformTenantSurfaceDeploymentChangedEvent,
			state.PlatformTenantCustomerLinkedEvent, state.PlatformTenantCustomerOffboardedEvent,
			state.PlatformTenantReconciliationAppliedEvent}, valid: true},
		{name: "empty", events: []string{}, valid: false},
		{name: "unknown", events: []string{"platform_tenant.hostname.failed"}, valid: false},
		{name: "duplicate", events: []string{state.PlatformTenantHostnameVerifiedEvent, state.PlatformTenantHostnameVerifiedEvent}, valid: false},
		{name: "too many", events: []string{state.PlatformTenantStatementFinalizedEvent, state.PlatformTenantHostnameVerifiedEvent,
			state.PlatformTenantSurfaceCertificateChangedEvent, state.PlatformTenantSurfaceDeploymentChangedEvent,
			state.PlatformTenantCustomerLinkedEvent, state.PlatformTenantCustomerOffboardedEvent,
			state.PlatformTenantReconciliationAppliedEvent, "unknown"}, valid: false},
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
	if state.ValidAppWebhookEvent(state.AppWebhookEvent(state.PlatformTenantSurfaceCertificateChangedEvent)) {
		t.Fatal("tenant-owned certificate event must not enter the app webhook vocabulary")
	}
	if state.ValidAppWebhookEvent(state.AppWebhookEvent(state.PlatformTenantSurfaceDeploymentChangedEvent)) {
		t.Fatal("tenant-owned deployment event must not enter the app webhook vocabulary")
	}
	if state.ValidAppWebhookEvent(state.AppWebhookEvent(state.PlatformTenantCustomerLinkedEvent)) ||
		state.ValidAppWebhookEvent(state.AppWebhookEvent(state.PlatformTenantCustomerOffboardedEvent)) {
		t.Fatal("tenant-owned customer events must not enter the app webhook vocabulary")
	}
	if state.ValidAppWebhookEvent(state.AppWebhookEvent(state.PlatformTenantReconciliationAppliedEvent)) {
		t.Fatal("tenant-owned reconciliation event must not enter the app webhook vocabulary")
	}
}

type platformTenantReconciliationWebhookFixture interface {
	state.Store
	state.PlatformTenantStore
	state.PlatformTenantReconciliationStore
	state.PlatformTenantWebhookStore
}

func TestMemPlatformTenantReconciliationAppliedWebhook(t *testing.T) {
	testPlatformTenantReconciliationAppliedWebhook(t, state.NewMemStore(), context.Background())
}

func TestPgPlatformTenantReconciliationAppliedWebhook(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	testPlatformTenantReconciliationAppliedWebhook(t, store, ctx)
}

func testPlatformTenantReconciliationAppliedWebhook(t *testing.T, store platformTenantReconciliationWebhookFixture, ctx context.Context) {
	t.Helper()
	accountID, _ := seedConsumerKeyAccountApp(t, ctx, store)
	tenant, _, err := store.CreatePlatformTenant(ctx, accountID, "reconcile-events-"+uuid.NewString()[:8], "Reconcile events", 250)
	if err != nil {
		t.Fatal(err)
	}
	limits := api.MustLimitsFor(api.PlanPro)
	hook, err := store.CreatePlatformTenantWebhookIfUnderQuota(ctx, state.AppWebhook{
		AccountID: accountID, PlatformTenantID: tenant.ID, Scope: state.AppWebhookScopePlatformTenant,
		TargetURL: "https://example.test/reconciliation-events", SecretSealed: []byte("sealed"),
		EventFilter: []string{state.PlatformTenantReconciliationAppliedEvent}, Enabled: true,
	}, limits)
	if err != nil {
		t.Fatal(err)
	}
	unrelatedHook, err := store.CreatePlatformTenantWebhookIfUnderQuota(ctx, state.AppWebhook{
		AccountID: accountID, PlatformTenantID: tenant.ID, Scope: state.AppWebhookScopePlatformTenant,
		TargetURL: "https://example.test/unrelated-events", SecretSealed: []byte("sealed"),
		EventFilter: []string{state.PlatformTenantCustomerLinkedEvent}, Enabled: true,
	}, limits)
	if err != nil {
		t.Fatal(err)
	}
	in := state.PlatformTenantReconciliationParams{TenantID: tenant.ID, ApplyPlatformTenantParams: state.ApplyPlatformTenantParams{
		AccountID: accountID, ExternalRef: tenant.ExternalRef, Name: tenant.Name, TenantLimit: 250, Limits: limits,
	}}
	plan, err := store.PlanPlatformTenantReconciliation(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.ApplyPlatformTenantReconciliation(ctx, in, plan.PlanHash)
	if err != nil || !first.Applied || first.ReceiptID == "" || first.AppliedAt.IsZero() {
		t.Fatalf("first apply = %+v, %v", first, err)
	}
	assertReconciliationAppliedDelivery := func(receipt api.PlatformTenantReconciliationApplyResponse) {
		t.Helper()
		deliveries, next, err := store.ListPlatformTenantWebhookDeliveries(ctx, accountID, tenant.ID, hook.ID, 50, "")
		if err != nil || next != "" || len(deliveries) != 1 {
			t.Fatalf("reconciliation deliveries = %d, next=%q, err=%v; want one", len(deliveries), next, err)
		}
		delivery := deliveries[0]
		if delivery.Event != state.AppWebhookEvent(state.PlatformTenantReconciliationAppliedEvent) || delivery.AppID != "" || delivery.AccountID != accountID {
			t.Fatalf("delivery metadata = %+v", delivery)
		}
		var payload api.PlatformTenantReconciliationAppliedWebhookPayload
		if err := json.Unmarshal(delivery.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.PlatformTenantID != tenant.ID || payload.ExternalRef != tenant.ExternalRef ||
			payload.ReceiptID != receipt.ReceiptID || payload.PlanHash != receipt.PlanHash ||
			!payload.AppliedAt.Equal(receipt.AppliedAt) || payload.ChangeCount != len(receipt.Changes) {
			t.Fatalf("reconciliation payload = %+v; receipt = %+v", payload, receipt)
		}
		if containsJSONStringKey(delivery.Payload, "changes") || containsJSONStringKey(delivery.Payload, "challenge_token") ||
			containsJSONStringKey(delivery.Payload, "desired_bundle") || containsJSONStringKey(delivery.Payload, "webhook_secret") {
			t.Fatalf("reconciliation event includes sensitive or duplicate receipt data: %s", delivery.Payload)
		}
	}
	assertReconciliationAppliedDelivery(first)
	if deliveries, next, err := store.ListPlatformTenantWebhookDeliveries(ctx, accountID, tenant.ID, unrelatedHook.ID, 50, ""); err != nil || next != "" || len(deliveries) != 0 {
		t.Fatalf("unrelated event deliveries = %d, next=%q, err=%v; want none", len(deliveries), next, err)
	}
	staleHash := strings.Repeat("0", 64)
	if staleHash == plan.PlanHash {
		staleHash = strings.Repeat("f", 64)
	}
	if _, err := store.ApplyPlatformTenantReconciliation(ctx, in, staleHash); !errors.Is(err, state.ErrPlatformTenantPlanStale) {
		t.Fatalf("stale apply error = %v, want stale plan", err)
	}
	if deliveries, _, err := store.ListPlatformTenantWebhookDeliveries(ctx, accountID, tenant.ID, hook.ID, 50, ""); err != nil || len(deliveries) != 1 {
		t.Fatalf("stale apply emitted a delivery: %d, %v", len(deliveries), err)
	}
	secondPlan, err := store.PlanPlatformTenantReconciliation(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.ApplyPlatformTenantReconciliation(ctx, in, secondPlan.PlanHash)
	if err != nil || second.ReceiptID == first.ReceiptID {
		t.Fatalf("second successful no-op apply = %+v, %v", second, err)
	}
	deliveries, next, err := store.ListPlatformTenantWebhookDeliveries(ctx, accountID, tenant.ID, hook.ID, 50, "")
	if err != nil || next != "" || len(deliveries) != 2 {
		t.Fatalf("successful no-op apply deliveries = %d, next=%q, err=%v; want two", len(deliveries), next, err)
	}
	seenReceipts := make(map[string]bool, len(deliveries))
	for _, delivery := range deliveries {
		if delivery.Event != state.AppWebhookEvent(state.PlatformTenantReconciliationAppliedEvent) {
			t.Fatalf("second batch event = %q", delivery.Event)
		}
		var payload api.PlatformTenantReconciliationAppliedWebhookPayload
		if err := json.Unmarshal(delivery.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		seenReceipts[payload.ReceiptID] = true
	}
	if !seenReceipts[first.ReceiptID] || !seenReceipts[second.ReceiptID] {
		t.Fatalf("delivery receipts = %v, want both successful apply receipts", seenReceipts)
	}
}

type platformTenantCustomerLifecycleWebhookFixture interface {
	state.Store
	state.PlatformTenantStore
	state.PlatformTenantWebhookStore
}

func TestMemPlatformTenantCustomerLifecycleWebhooks(t *testing.T) {
	testPlatformTenantCustomerLifecycleWebhooks(t, state.NewMemStore(), context.Background())
}

func TestPgPlatformTenantCustomerLifecycleWebhooks(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	testPlatformTenantCustomerLifecycleWebhooks(t, store, ctx)
}

func testPlatformTenantCustomerLifecycleWebhooks(t *testing.T, store platformTenantCustomerLifecycleWebhookFixture, ctx context.Context) {
	t.Helper()
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	tenant, _, err := store.CreatePlatformTenant(ctx, accountID, "customer-events-"+uuid.NewString()[:8], "Customer events", 250)
	if err != nil {
		t.Fatal(err)
	}
	hook, err := store.CreatePlatformTenantWebhookIfUnderQuota(ctx, state.AppWebhook{
		AccountID: accountID, PlatformTenantID: tenant.ID, Scope: state.AppWebhookScopePlatformTenant,
		TargetURL: "https://example.test/customer-events", SecretSealed: []byte("sealed"),
		EventFilter: []string{state.PlatformTenantCustomerLinkedEvent, state.PlatformTenantCustomerOffboardedEvent}, Enabled: true,
	}, api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := store.CreateAPIConsumer(ctx, accountID, appID, "customer-42", "Customer 42")
	if err != nil {
		t.Fatal(err)
	}
	if deliveries, _, err := store.ListPlatformTenantWebhookDeliveries(ctx, accountID, tenant.ID, hook.ID, 50, ""); err != nil || len(deliveries) != 0 {
		t.Fatalf("unlinked consumer deliveries = %d, err=%v; want none", len(deliveries), err)
	}
	if _, err := store.LinkPlatformTenantConsumer(ctx, accountID, tenant.ID, consumer.ID); err != nil {
		t.Fatal(err)
	}
	// Repeating a link is a no-op, not a second lifecycle event.
	if _, err := store.LinkPlatformTenantConsumer(ctx, accountID, tenant.ID, consumer.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RevokeAPIConsumer(ctx, accountID, consumer.ID); err != nil {
		t.Fatal(err)
	}
	// An idempotent revoke is likewise not a new transition.
	if _, err := store.RevokeAPIConsumer(ctx, accountID, consumer.ID); err != nil {
		t.Fatal(err)
	}
	deliveries, next, err := store.ListPlatformTenantWebhookDeliveries(ctx, accountID, tenant.ID, hook.ID, 50, "")
	if err != nil {
		t.Fatal(err)
	}
	if next != "" || len(deliveries) != 2 {
		t.Fatalf("deliveries = %d, next=%q; want one linked and one offboarded delivery", len(deliveries), next)
	}
	seen := map[state.AppWebhookEvent]bool{}
	for _, delivery := range deliveries {
		if delivery.AppID != "" || delivery.AccountID != accountID || seen[delivery.Event] {
			t.Fatalf("unexpected delivery metadata or duplicate event: %+v", delivery)
		}
		seen[delivery.Event] = true
		var payload api.PlatformTenantCustomerLifecycleWebhookPayload
		if err := json.Unmarshal(delivery.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.PlatformTenantID != tenant.ID || payload.ExternalRef != tenant.ExternalRef ||
			payload.ConsumerID != consumer.ID || payload.AppID != appID ||
			payload.CustomerExternalRef != consumer.ExternalRef || payload.CustomerName != consumer.Name || payload.ChangedAt.IsZero() {
			t.Fatalf("event %q payload = %+v", delivery.Event, payload)
		}
		switch delivery.Event {
		case state.AppWebhookEvent(state.PlatformTenantCustomerLinkedEvent):
			if payload.CustomerStatus != string(state.APIConsumerStatusActive) {
				t.Fatalf("linked customer_status = %q", payload.CustomerStatus)
			}
		case state.AppWebhookEvent(state.PlatformTenantCustomerOffboardedEvent):
			if payload.CustomerStatus != string(state.APIConsumerStatusRevoked) {
				t.Fatalf("offboarded customer_status = %q", payload.CustomerStatus)
			}
		default:
			t.Fatalf("unexpected event %q", delivery.Event)
		}
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

func TestPgPlatformTenantSurfaceCertificateChangedWebhook(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	tenant, _, err := store.CreatePlatformTenant(ctx, accountID, "cert-customer-"+uuid.NewString()[:8], "Certificate customer", 250)
	if err != nil {
		t.Fatal(err)
	}
	limits := api.MustLimitsFor(api.PlanPro)
	surface, err := store.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{
		AccountID: accountID, AppID: appID, Name: "customer-certificate",
	}, limits)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.LinkPlatformTenantSurface(ctx, accountID, tenant.ID, surface.ID); err != nil {
		t.Fatal(err)
	}
	hook, err := store.CreatePlatformTenantWebhookIfUnderQuota(ctx, state.AppWebhook{
		AccountID: accountID, PlatformTenantID: tenant.ID, Scope: state.AppWebhookScopePlatformTenant,
		TargetURL: "https://example.com/certificate-events", SecretSealed: []byte("sealed"),
		EventFilter: []string{
			state.PlatformTenantStatementFinalizedEvent,
			state.PlatformTenantHostnameVerifiedEvent,
			state.PlatformTenantSurfaceCertificateChangedEvent,
		}, Enabled: true,
	}, limits)
	if err != nil {
		t.Fatal(err)
	}

	expiresAt := time.Now().UTC().Add(90 * 24 * time.Hour).Truncate(time.Second)
	for _, transition := range []struct {
		state state.CertState
		err   string
		until time.Time
	}{
		{state: state.CertStatePending},
		{state: state.CertStateFailed, err: "provider diagnostic must not be copied into the event"},
		// Re-entry with an unchanged state must be a no-op and enqueue nothing.
		{state: state.CertStateFailed, err: "replacement provider diagnostic is also private"},
		{state: state.CertStateIssued, until: expiresAt},
	} {
		if err := store.UpdateTenantSurfaceCert(ctx, state.UpdateSurfaceCertParams{
			SurfaceID: surface.ID, CertState: transition.state, LastError: transition.err, NotAfter: transition.until,
		}); err != nil {
			t.Fatal(err)
		}
	}

	// A certificate transition on a surface outside this tenant must not be
	// delivered to the tenant receiver, even within the same account and app.
	unlinkedSurface, err := store.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{
		AccountID: accountID, AppID: appID, Name: "unlinked-certificate",
	}, limits)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateTenantSurfaceCert(ctx, state.UpdateSurfaceCertParams{
		SurfaceID: unlinkedSurface.ID, CertState: state.CertStateFailed, LastError: "private provider error",
	}); err != nil {
		t.Fatal(err)
	}

	deliveries, next, err := store.ListPlatformTenantWebhookDeliveries(ctx, accountID, tenant.ID, hook.ID, 50, "")
	if err != nil {
		t.Fatal(err)
	}
	if next != "" || len(deliveries) != 3 {
		t.Fatalf("deliveries = %d, next = %q; want one delivery for each of three state transitions", len(deliveries), next)
	}
	seenStates := map[string]int{}
	for _, delivery := range deliveries {
		if delivery.Event != state.PlatformTenantSurfaceCertificateChangedEvent {
			t.Fatalf("event = %q", delivery.Event)
		}
		var payload api.PlatformTenantSurfaceCertificateChangedWebhookPayload
		if err := json.Unmarshal(delivery.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.PlatformTenantID != tenant.ID || payload.ExternalRef != tenant.ExternalRef ||
			payload.SurfaceID != surface.ID || payload.SurfaceName != surface.Name || payload.AppID != appID || payload.ChangedAt.IsZero() {
			t.Fatalf("payload = %+v", payload)
		}
		seenStates[payload.CertState]++
		if payload.CertState == string(state.CertStateIssued) {
			if payload.CertNotAfter == nil || !payload.CertNotAfter.Equal(expiresAt) {
				t.Fatalf("issued cert_not_after = %v, want %s", payload.CertNotAfter, expiresAt)
			}
		} else if payload.CertNotAfter != nil {
			t.Fatalf("non-issued cert state %q unexpectedly has expiry %s", payload.CertState, payload.CertNotAfter)
		}
		if containsJSONStringKey(delivery.Payload, "cert_last_error") ||
			containsJSONStringKey(delivery.Payload, "private_key") ||
			containsJSONStringKey(delivery.Payload, "certificate_pem") ||
			containsJSONStringKey(delivery.Payload, "challenge_token") {
			t.Fatalf("certificate event exposes private material: %s", delivery.Payload)
		}
		if containsJSONStringValue(delivery.Payload, "provider diagnostic") {
			t.Fatalf("certificate event exposes provider error text: %s", delivery.Payload)
		}
	}
	for _, certState := range []string{string(state.CertStatePending), string(state.CertStateFailed), string(state.CertStateIssued)} {
		if seenStates[certState] != 1 {
			t.Errorf("state %q deliveries = %d, want 1", certState, seenStates[certState])
		}
	}
}

func TestPgPlatformTenantSurfaceDeploymentChangedWebhook(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	tenant, _, err := store.CreatePlatformTenant(ctx, accountID, "deploy-customer-"+uuid.NewString()[:8], "Deployment customer", 250)
	if err != nil {
		t.Fatal(err)
	}
	otherTenant, _, err := store.CreatePlatformTenant(ctx, accountID, "other-deploy-customer-"+uuid.NewString()[:8], "Other deployment customer", 250)
	if err != nil {
		t.Fatal(err)
	}
	limits := api.MustLimitsFor(api.PlanPro)
	linkedSurface, err := store.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{
		AccountID: accountID, AppID: appID, Name: "deployment-customer",
	}, limits)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.LinkPlatformTenantSurface(ctx, accountID, tenant.ID, linkedSurface.ID); err != nil {
		t.Fatal(err)
	}
	otherSurface, err := store.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{
		AccountID: accountID, AppID: appID, Name: "other-deployment-customer",
	}, limits)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.LinkPlatformTenantSurface(ctx, accountID, otherTenant.ID, otherSurface.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{
		AccountID: accountID, AppID: appID, Name: "unlinked-deployment-customer",
	}, limits); err != nil {
		t.Fatal(err)
	}

	createHook := func(externalRef string) state.AppWebhook {
		t.Helper()
		hook, err := store.CreatePlatformTenantWebhookIfUnderQuota(ctx, state.AppWebhook{
			AccountID: accountID, PlatformTenantID: externalRef, Scope: state.AppWebhookScopePlatformTenant,
			TargetURL: "https://example.com/deployment-events/" + uuid.NewString(), SecretSealed: []byte("sealed"),
			EventFilter: []string{state.PlatformTenantSurfaceDeploymentChangedEvent}, Enabled: true,
		}, limits)
		if err != nil {
			t.Fatal(err)
		}
		return hook
	}
	hook, otherHook := createHook(tenant.ID), createHook(otherTenant.ID)

	deployment, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: appID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:private-image-digest",
		CommitSHA: "0123456789abcdef0123456789abcdef01234567", Status: state.DeployPending,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateDeploymentStatus(ctx, deployment.ID, state.DeployBuilding, ""); err != nil {
		t.Fatal(err)
	}
	if got, _, err := store.ListPlatformTenantWebhookDeliveries(ctx, accountID, tenant.ID, hook.ID, 50, ""); err != nil || len(got) != 0 {
		t.Fatalf("non-terminal deployment deliveries = %d, err=%v; want none", len(got), err)
	}
	if err := store.UpdateDeploymentStatus(ctx, deployment.ID, state.DeployFailed, "private build failure details"); err != nil {
		t.Fatal(err)
	}
	// Repeating the same terminal status write must not duplicate the event.
	if err := store.UpdateDeploymentStatus(ctx, deployment.ID, state.DeployFailed, "replacement private build failure"); err != nil {
		t.Fatal(err)
	}
	liveDeployment, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: appID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:another-private-image", Status: state.DeployPending,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateDeploymentStatus(ctx, liveDeployment.ID, state.DeployLive, ""); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		tenantID    string
		hookID      string
		surfaceID   string
		surfaceName string
		external    string
	}{{tenant.ID, hook.ID, linkedSurface.ID, linkedSurface.Name, tenant.ExternalRef}, {otherTenant.ID, otherHook.ID, otherSurface.ID, otherSurface.Name, otherTenant.ExternalRef}} {
		deliveries, next, err := store.ListPlatformTenantWebhookDeliveries(ctx, accountID, tc.tenantID, tc.hookID, 50, "")
		if err != nil {
			t.Fatal(err)
		}
		if next != "" || len(deliveries) != 2 {
			t.Fatalf("tenant %s deliveries = %d, next=%q; want one event per terminal transition for its linked surface", tc.tenantID, len(deliveries), next)
		}
		seen := map[string]int{}
		for _, delivery := range deliveries {
			if delivery.Event != state.PlatformTenantSurfaceDeploymentChangedEvent || delivery.AppID != "" {
				t.Fatalf("event/app id = %q/%q", delivery.Event, delivery.AppID)
			}
			var payload api.PlatformTenantSurfaceDeploymentChangedWebhookPayload
			if err := json.Unmarshal(delivery.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			wantDeployment := deployment
			wantStatus := state.DeployFailed
			if payload.DeploymentStatus == string(state.DeployLive) {
				wantDeployment = liveDeployment
				wantStatus = state.DeployLive
			}
			if payload.PlatformTenantID != tc.tenantID || payload.ExternalRef != tc.external ||
				payload.SurfaceID != tc.surfaceID || payload.SurfaceName != tc.surfaceName || payload.Revision != wantDeployment.Revision ||
				payload.DeploymentStatus != string(wantStatus) || !payload.StartedAt.Equal(wantDeployment.CreatedAt) ||
				payload.ChangedAt.IsZero() {
				t.Fatalf("deployment event payload = %+v; want tenant=%s external_ref=%s surface=%s/%s status=%s deployment=%+v changed_at_set=%t", payload, tc.tenantID, tc.external, tc.surfaceID, tc.surfaceName, wantStatus, wantDeployment, !payload.ChangedAt.IsZero())
			}
			seen[payload.DeploymentStatus]++
			for _, forbidden := range []string{appID, deployment.ID, liveDeployment.ID, "private-image-digest", "another-private-image", "0123456789abcdef", "private build failure"} {
				if strings.Contains(string(delivery.Payload), forbidden) {
					t.Errorf("deployment payload leaked %q: %s", forbidden, delivery.Payload)
				}
			}
		}
		if seen[string(state.DeployFailed)] != 1 || seen[string(state.DeployLive)] != 1 {
			t.Errorf("tenant %s terminal outcomes = %v, want one live and one failed", tc.tenantID, seen)
		}
	}

	otherApp, err := store.CreateApp(ctx, state.App{
		AccountID: accountID, Slug: "unlinked-" + uuid.NewString()[:8], Type: state.AppTypeApp,
		RAMMB: 256, MaxConcurrency: 2, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	unlinkedDeployment, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: otherApp.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:unlinked", Status: state.DeployPending,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateDeploymentStatus(ctx, unlinkedDeployment.ID, state.DeployLive, ""); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ tenantID, hookID string }{{tenant.ID, hook.ID}, {otherTenant.ID, otherHook.ID}} {
		deliveries, _, err := store.ListPlatformTenantWebhookDeliveries(ctx, accountID, tc.tenantID, tc.hookID, 50, "")
		if err != nil || len(deliveries) != 2 {
			t.Fatalf("unlinked deployment changed tenant %s delivery count to %d, err=%v", tc.tenantID, len(deliveries), err)
		}
	}

	rolledBackDeployment, err := store.CreateDeployment(ctx, state.Deployment{
		// Keep this row out of the default scope, which already has a live
		// deployment above; the database permits only one live row per app/scope.
		AppID: appID, Scope: "rollback-test", Kind: state.DeploymentKindImage, ImageDigest: "sha256:rollback", Status: state.DeployPending,
	})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `update deployments set status='live' where id=$1`, rolledBackDeployment.ID); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ tenantID, hookID string }{{tenant.ID, hook.ID}, {otherTenant.ID, otherHook.ID}} {
		deliveries, _, err := store.ListPlatformTenantWebhookDeliveries(ctx, accountID, tc.tenantID, tc.hookID, 50, "")
		if err != nil || len(deliveries) != 2 {
			t.Fatalf("rolled-back deployment changed tenant %s delivery count to %d, err=%v", tc.tenantID, len(deliveries), err)
		}
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

func containsJSONStringValue(body json.RawMessage, value string) bool {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(body, &object); err != nil {
		return false
	}
	for _, raw := range object {
		var got string
		if json.Unmarshal(raw, &got) == nil && strings.Contains(got, value) {
			return true
		}
	}
	return false
}
