package conformance

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// testNonpaymentMarkerSurvivesTheQuotaStop pins billing review 2026-11-13
// (R9-1): an account moved to Free for non-payment is active with
// past_due_at set, and the Free quota stop and its lifting keep that
// unpaid-balance marker. Only a paid order or an operator clears it.
func testNonpaymentMarkerSurvivesTheQuotaStop(t *testing.T, fx *Fixture) {
	s, ctx, id := fx.Store, fx.Ctx, fx.Account.ID
	status := func() state.Account {
		t.Helper()
		acct, err := s.AccountByID(ctx, id)
		if err != nil {
			t.Fatalf("AccountByID: %v", err)
		}
		return acct
	}
	if err := s.MarkDunningStep(ctx, id, state.AccountActive, state.AccountPastDue); err != nil {
		t.Fatalf("MarkDunningStep(active→past_due): %v", err)
	}
	if err := s.MarkDunningStep(ctx, id, state.AccountPastDue, state.AccountActive); err != nil {
		t.Fatalf("MarkDunningStep(past_due→active): %v", err)
	}
	if got := status(); !got.MovedToFreeForNonpayment() {
		t.Fatalf("after the move: status %s past_due_at %v; want active with the balance marked", got.Status, got.PastDueAt)
	}
	if ok, err := s.SuspendAccountForFreeQuota(ctx, id); err != nil || !ok {
		t.Fatalf("SuspendAccountForFreeQuota = %v, %v; want true", ok, err)
	}
	if got := status(); got.Status != state.AccountSuspended || got.PastDueAt == nil {
		t.Fatalf("under the quota stop: status %s past_due_at %v; want suspended with the balance marked", got.Status, got.PastDueAt)
	}
	if ok, err := s.RestoreFreeQuotaSuspension(ctx, id); err != nil || !ok {
		t.Fatalf("RestoreFreeQuotaSuspension = %v, %v; want true", ok, err)
	}
	if got := status(); !got.MovedToFreeForNonpayment() {
		t.Fatalf("after the quota stop: status %s past_due_at %v; want active with the balance marked", got.Status, got.PastDueAt)
	}
	if err := s.UpdateAccountStatus(ctx, id, state.AccountActive); err != nil {
		t.Fatalf("UpdateAccountStatus(active): %v", err)
	}
	if got := status(); got.PastDueAt != nil {
		t.Fatalf("a restore kept past_due_at %v", got.PastDueAt)
	}
}
