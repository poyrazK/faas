package conformance

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// testPendingBillingSkipsFreeSubjects pins ADR-20261031100000: a subject that
// cancelled keeps its billing identity, but its hours are never delivered, so
// they are not pending; a paying subject's hours are.
func testPendingBillingSkipsFreeSubjects(t *testing.T, fx *Fixture) {
	s, ctx := fx.Store, fx.Ctx
	hour := time.Now().UTC().Truncate(time.Hour).Add(-2 * time.Hour)
	ids := map[api.Plan]string{}
	for _, plan := range []api.Plan{api.PlanPro, api.PlanFree} {
		acct, err := s.CreateAccount(ctx, "pending-"+string(plan)+"-"+uuid.NewString()+"@example.com", api.PlanPro)
		if err != nil {
			t.Fatalf("CreateAccount: %v", err)
		}
		if err := s.UpsertBillingIdentity(ctx, state.BillingIdentity{AccountID: acct.ID, Provider: "polar", CustomerID: "cus-" + uuid.NewString(), BillingFrom: hour}); err != nil {
			t.Fatalf("UpsertBillingIdentity: %v", err)
		}
		if err := s.AppendUsage(ctx, acct.ID, uuid.NewString(), uuid.NewString(), hour.Add(time.Minute), 1_000, 0, 0, 0, 0, 0, 0, 0); err != nil {
			t.Fatalf("AppendUsage: %v", err)
		}
		if plan == api.PlanFree {
			if err := s.UpdateAccountPlan(ctx, acct.ID, api.PlanFree); err != nil {
				t.Fatalf("UpdateAccountPlan(free): %v", err)
			}
		}
		ids[plan] = acct.ID
	}
	pending, err := s.PendingBillingUsageWindows(ctx, "polar", hour, hour.Add(time.Hour))
	if err != nil {
		t.Fatalf("PendingBillingUsageWindows: %v", err)
	}
	found := map[string]bool{}
	for _, w := range pending {
		found[w.AccountID] = true
	}
	if !found[ids[api.PlanPro]] {
		t.Fatalf("paying account's hour is not pending: %+v", pending)
	}
	if found[ids[api.PlanFree]] {
		t.Fatalf("cancelled (Free) account's hour is pending: %+v", pending)
	}
}
