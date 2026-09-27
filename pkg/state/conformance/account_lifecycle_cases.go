package conformance

import (
	"errors"
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

// testDunningDeletionIsScheduledAndPaidBack pins the end of the dunning
// ladder: the move to deleted_pending stamps deletion_requested_at — the
// anchor the grace sweep purges on and the deletion email promises — and
// the self-service restore refuses it (paying is what undoes a dunning
// deletion). Any return to active clears both anchors.
func testDunningDeletionIsScheduledAndPaidBack(t *testing.T, fx *Fixture) {
	s, ctx, id := fx.Store, fx.Ctx, fx.Account.ID
	steps := [][2]state.AccountStatus{
		{state.AccountActive, state.AccountPastDue},
		{state.AccountPastDue, state.AccountSuspended},
		{state.AccountSuspended, state.AccountDeletedPending},
	}
	for _, step := range steps {
		if err := s.MarkDunningStep(ctx, id, step[0], step[1]); err != nil {
			t.Fatalf("MarkDunningStep(%s→%s): %v", step[0], step[1], err)
		}
	}
	acct, err := s.AccountByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if acct.DeletionRequestedAt == nil {
		t.Fatal("dunning moved the account to deleted_pending without deletion_requested_at: the grace sweep would never purge it")
	}
	if err := s.RestoreAccount(ctx, id); err == nil {
		t.Fatal("self-service restore undid a dunning deletion without payment")
	}
	if err := s.UpdateAccountStatus(ctx, id, state.AccountActive); err != nil { // payment received
		t.Fatalf("UpdateAccountStatus(active): %v", err)
	}
	acct, err = s.AccountByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if acct.Status != state.AccountActive || acct.PastDueAt != nil || acct.DeletionRequestedAt != nil {
		t.Fatalf("after payment: status %s, past_due_at %v, deletion_requested_at %v; want active with both cleared",
			acct.Status, acct.PastDueAt, acct.DeletionRequestedAt)
	}
}

// testSelfServiceDeletionOnlyFromActive pins MarkAccountDeletionPending's
// status gate. Postgres only schedules an active (or already pending)
// account; MemStore scheduled any status, so no test saw the production
// refusal a past_due customer hit.
func testSelfServiceDeletionOnlyFromActive(t *testing.T, fx *Fixture) {
	s, ctx, id := fx.Store, fx.Ctx, fx.Account.ID
	if err := s.MarkDunningStep(ctx, id, state.AccountActive, state.AccountPastDue); err != nil {
		t.Fatalf("MarkDunningStep(active→past_due): %v", err)
	}
	if err := s.MarkAccountDeletionPending(ctx, id); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("MarkAccountDeletionPending on past_due: err = %v, want ErrNotFound", err)
	}
	if acct, err := s.AccountByID(ctx, id); err != nil || acct.Status != state.AccountPastDue || acct.DeletionRequestedAt != nil {
		t.Fatalf("after refused deletion: %+v, %v; want past_due with no deletion stamp", acct, err)
	}
	if err := s.UpdateAccountStatus(ctx, id, state.AccountActive); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkAccountDeletionPending(ctx, id); err != nil {
		t.Fatalf("MarkAccountDeletionPending on active: %v", err)
	}
	first, err := s.AccountByID(ctx, id)
	if err != nil || first.Status != state.AccountDeletedPending || first.DeletionRequestedAt == nil {
		t.Fatalf("after deletion: %+v, %v; want deleted_pending with a stamp", first, err)
	}
	if err := s.MarkAccountDeletionPending(ctx, id); err != nil {
		t.Fatalf("repeat MarkAccountDeletionPending: %v", err)
	}
	if again, _ := s.AccountByID(ctx, id); again.DeletionRequestedAt == nil || !again.DeletionRequestedAt.Equal(*first.DeletionRequestedAt) {
		t.Fatalf("repeat call moved the grace anchor: %v → %v", first.DeletionRequestedAt, again.DeletionRequestedAt)
	}
}
