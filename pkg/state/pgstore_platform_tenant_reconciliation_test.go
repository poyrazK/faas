package state_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgPlatformTenantReconciliationApplyRemovesOnlyManagedResources(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	externalRef := "reconcile-" + uuid.NewString()
	keepHostname := "keep-" + uuid.NewString()[:8] + ".example.test"
	removeHostname := "remove-" + uuid.NewString()[:8] + ".example.test"
	created, err := store.ApplyPlatformTenant(ctx, state.ApplyPlatformTenantParams{
		AccountID: accountID, ExternalRef: externalRef, Name: "Reconcile Customer", TenantLimit: 250,
		Limits: api.MustLimitsFor(api.PlanPro),
		Consumers: []state.ApplyPlatformTenantConsumer{
			{AppID: appID, ExternalRef: "keep", Name: "Keep"},
			{AppID: appID, ExternalRef: "remove", Name: "Remove"},
		},
		Surfaces: []state.ApplyPlatformTenantSurface{{AppID: appID, Name: "reconcile-" + uuid.NewString()[:8],
			CertKind: state.CertKindPerHostSAN, Hostnames: []state.ApplyPlatformTenantHostname{
				{Hostname: keepHostname, ChallengeToken: "keep-token"},
				{Hostname: removeHostname, ChallengeToken: "remove-token"},
			}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Tenant.ID == "" || created.Surfaces[0].Surface.ID == "" {
		t.Fatalf("initial apply = %+v", created)
	}

	in := state.PlatformTenantReconciliationParams{TenantID: created.Tenant.ID, ApplyPlatformTenantParams: state.ApplyPlatformTenantParams{
		AccountID: accountID, ExternalRef: externalRef, Name: "Reconcile Customer", TenantLimit: 250,
		Limits:    api.MustLimitsFor(api.PlanPro),
		Consumers: []state.ApplyPlatformTenantConsumer{{AppID: appID, ExternalRef: "keep", Name: "Keep"}},
		Surfaces: []state.ApplyPlatformTenantSurface{{AppID: appID, Name: created.Surfaces[0].Surface.Name,
			CertKind: state.CertKindPerHostSAN, Hostnames: []state.ApplyPlatformTenantHostname{{Hostname: keepHostname, ChallengeToken: "keep-token"}}}},
	}}
	plan, err := store.PlanPlatformTenantReconciliation(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if plan.PlanHash == "" || !hasPlatformTenantReconciliationChange(plan.Changes, "consumer", "remove_candidate", "remove", "") ||
		!hasPlatformTenantReconciliationChange(plan.Changes, "hostname", "remove_candidate", "", removeHostname) {
		t.Fatalf("plan = %+v", plan)
	}
	applied, err := store.ApplyPlatformTenantReconciliation(ctx, in, plan.PlanHash)
	if err != nil {
		t.Fatal(err)
	}
	if !applied.Applied || applied.ReceiptID == "" || applied.AppliedAt.IsZero() || applied.PlanHash != plan.PlanHash ||
		!hasPlatformTenantReconciliationChange(applied.Changes, "consumer", "detached", "remove", "") ||
		!hasPlatformTenantReconciliationChange(applied.Changes, "hostname", "removed", "", removeHostname) {
		t.Fatalf("applied = %+v", applied)
	}
	receipts, nextToken, err := store.ListPlatformTenantReconciliationReceipts(ctx, accountID, created.Tenant.ID, 10, "")
	if err != nil || len(receipts) != 1 || nextToken != "" || receipts[0].ReceiptID != applied.ReceiptID || receipts[0].ChangeCount != len(applied.Changes) {
		t.Fatalf("reconciliation receipts = %+v, next=%q, %v", receipts, nextToken, err)
	}
	receipt, err := store.GetPlatformTenantReconciliationReceipt(ctx, accountID, created.Tenant.ID, applied.ReceiptID)
	if err != nil || receipt.PlanHash != plan.PlanHash || len(receipt.Changes) != len(applied.Changes) {
		t.Fatalf("reconciliation receipt = %+v, %v", receipt, err)
	}
	foreign, err := store.GetPlatformTenantReconciliationReceipt(ctx, accountID, uuid.NewString(), applied.ReceiptID)
	if !errors.Is(err, state.ErrNotFound) || foreign.ReceiptID != "" {
		t.Fatalf("foreign tenant receipt = %+v, %v; want not found", foreign, err)
	}
	planAgain, err := store.PlanPlatformTenantReconciliation(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	appliedAgain, err := store.ApplyPlatformTenantReconciliation(ctx, in, planAgain.PlanHash)
	if err != nil || appliedAgain.ReceiptID == applied.ReceiptID {
		t.Fatalf("second reconciliation = %+v, %v", appliedAgain, err)
	}
	latest, next, err := store.ListPlatformTenantReconciliationReceipts(ctx, accountID, created.Tenant.ID, 1, "")
	if err != nil || len(latest) != 1 || latest[0].ReceiptID != appliedAgain.ReceiptID || next == "" {
		t.Fatalf("latest receipt page = %+v, next=%q, %v", latest, next, err)
	}
	previous, next, err := store.ListPlatformTenantReconciliationReceipts(ctx, accountID, created.Tenant.ID, 1, next)
	if err != nil || len(previous) != 1 || previous[0].ReceiptID != applied.ReceiptID || next != "" {
		t.Fatalf("previous receipt page = %+v, next=%q, %v", previous, next, err)
	}
	consumers, err := store.ListPlatformTenantConsumers(ctx, accountID, created.Tenant.ID)
	if err != nil || len(consumers) != 1 || consumers[0].ExternalRef != "keep" {
		t.Fatalf("linked consumers = %+v, %v", consumers, err)
	}
	hostnames, err := store.ListTenantHostnamesForSurface(ctx, created.Surfaces[0].Surface.ID)
	if err != nil || len(hostnames) != 1 || hostnames[0].Hostname != keepHostname {
		t.Fatalf("surface hostnames = %+v, %v", hostnames, err)
	}

	staleInput := in
	staleInput.Consumers = nil
	staleInput.Surfaces = nil
	stalePlan, err := store.PlanPlatformTenantReconciliation(ctx, staleInput)
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateAPIConsumer(ctx, accountID, appID, "joined-after-preview", "Joined After Preview")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.LinkPlatformTenantConsumer(ctx, accountID, created.Tenant.ID, other.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyPlatformTenantReconciliation(ctx, staleInput, stalePlan.PlanHash); !errors.Is(err, state.ErrPlatformTenantPlanStale) {
		t.Fatalf("stale apply = %v", err)
	}
	consumers, err = store.ListPlatformTenantConsumers(ctx, accountID, created.Tenant.ID)
	if err != nil || len(consumers) != 2 {
		t.Fatalf("stale apply partially changed links: %+v, %v", consumers, err)
	}
}

func hasPlatformTenantReconciliationChange(changes []api.PlatformTenantReconciliationPlanChange, resourceType, action, externalRef, hostname string) bool {
	for _, change := range changes {
		if change.ResourceType == resourceType && change.Action == action && change.ExternalRef == externalRef && change.Hostname == hostname {
			return true
		}
	}
	return false
}
