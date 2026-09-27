package conformance

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// testAccountLifecycleLeavesTheDunningLadder pins how an account enters and
// leaves the two automatic suspensions:
//
//   - A status change outside the dunning ladder clears past_due_at, so a
//     later payment failure starts a fresh grace period and an operator
//     suspension is never advanced to deletion by the dunning timer.
//   - The Free quota stop applies only to an active account and is lifted
//     only by RestoreFreeQuotaSuspension; operator suspensions are not.
func testAccountLifecycleLeavesTheDunningLadder(t *testing.T, fx *Fixture) {
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
	if status().PastDueAt == nil {
		t.Fatal("entering past_due did not stamp past_due_at")
	}
	if err := s.UpdateAccountStatus(ctx, id, state.AccountActive); err != nil {
		t.Fatalf("UpdateAccountStatus(active): %v", err)
	}
	if got := status(); got.Status != state.AccountActive || got.PastDueAt != nil {
		t.Fatalf("after recovery: status %s, past_due_at %v; want active with past_due_at cleared", got.Status, got.PastDueAt)
	}

	// Free quota stop: applies to an active account, and only once.
	if ok, err := s.SuspendAccountForFreeQuota(ctx, id); err != nil || !ok {
		t.Fatalf("SuspendAccountForFreeQuota(active) = %v, %v; want true", ok, err)
	}
	if ok, err := s.SuspendAccountForFreeQuota(ctx, id); err != nil || ok {
		t.Fatalf("SuspendAccountForFreeQuota(suspended) = %v, %v; want false", ok, err)
	}
	if got := status(); got.Status != state.AccountSuspended {
		t.Fatalf("after quota stop: status %s, want suspended", got.Status)
	}
	if ok, err := s.RestoreFreeQuotaSuspension(ctx, id); err != nil || !ok {
		t.Fatalf("RestoreFreeQuotaSuspension(quota stop) = %v, %v; want true", ok, err)
	}
	if got := status(); got.Status != state.AccountActive {
		t.Fatalf("after lifting the quota stop: status %s, want active", got.Status)
	}

	// An operator suspension is not a quota stop.
	if err := s.UpdateAccountStatus(ctx, id, state.AccountSuspended); err != nil {
		t.Fatalf("UpdateAccountStatus(suspended): %v", err)
	}
	if ok, err := s.RestoreFreeQuotaSuspension(ctx, id); err != nil || ok {
		t.Fatalf("RestoreFreeQuotaSuspension(operator suspension) = %v, %v; want false", ok, err)
	}
	if got := status(); got.Status != state.AccountSuspended {
		t.Fatalf("operator suspension was lifted: status %s", got.Status)
	}
}
