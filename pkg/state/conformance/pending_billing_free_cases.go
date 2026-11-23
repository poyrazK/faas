package conformance

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// testPendingBillingSkipsFreeSubjects pins ADR-20261031100000 as amended by
// ADR-20261121231500: a Free subject that never paid keeps nothing pending,
// a paying subject's hours are pending, and a subject that cancelled still
// lists its hours from before the plan change that made it Free, so a paid
// hour not yet delivered when the subscription ended reaches the pusher.
// (The case predated ADR-20261121231500 and expected the cancelled subject's
// paid hour to be skipped; #7797 changed both stores but not this case.)
func testPendingBillingSkipsFreeSubjects(t *testing.T, fx *Fixture) {
	s, ctx := fx.Store, fx.Ctx
	hour := time.Now().UTC().Truncate(time.Hour).Add(-2 * time.Hour)
	subject := func(name string, created api.Plan, downgrade bool) string {
		t.Helper()
		acct, err := s.CreateAccount(ctx, "pending-"+name+"-"+uuid.NewString()+"@example.com", created)
		if err != nil {
			t.Fatalf("CreateAccount(%s): %v", name, err)
		}
		if err := s.UpsertBillingIdentity(ctx, state.BillingIdentity{AccountID: acct.ID, Provider: "polar", CustomerID: "cus-" + uuid.NewString(), BillingFrom: hour}); err != nil {
			t.Fatalf("UpsertBillingIdentity(%s): %v", name, err)
		}
		if err := s.AppendUsage(ctx, acct.ID, uuid.NewString(), uuid.NewString(), hour.Add(time.Minute), 1_000, 0, 0, 0, 0, 0, 0, 0); err != nil {
			t.Fatalf("AppendUsage(%s): %v", name, err)
		}
		if downgrade {
			if err := s.UpdateAccountPlan(ctx, acct.ID, api.PlanFree); err != nil {
				t.Fatalf("UpdateAccountPlan(%s, free): %v", name, err)
			}
		}
		return acct.ID
	}
	paying := subject("pro", api.PlanPro, false)
	cancelled := subject("cancelled", api.PlanPro, true)
	neverPaid := subject("free", api.PlanFree, false)

	pending, err := s.PendingBillingUsageWindows(ctx, "polar", hour, hour.Add(time.Hour))
	if err != nil {
		t.Fatalf("PendingBillingUsageWindows: %v", err)
	}
	found := map[string]bool{}
	for _, w := range pending {
		found[w.AccountID] = true
	}
	if !found[paying] {
		t.Fatalf("paying account's hour is not pending: %+v", pending)
	}
	if !found[cancelled] {
		t.Fatalf("cancelled account's paid hour before the downgrade is not pending: %+v", pending)
	}
	if found[neverPaid] {
		t.Fatalf("never-paid Free account's hour is pending: %+v", pending)
	}
}
