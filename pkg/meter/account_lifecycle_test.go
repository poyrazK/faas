// spec: §4.7
package meter_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
)

func enforceQuota(t *testing.T, ctx context.Context, s *state.MemStore, id string, usedGB float64) state.Account {
	t.Helper()
	acct, err := s.AccountByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := meter.EnforceQuota(ctx, s, &fakeNotifier{}, &fakeParker{}, &fakeMailer{}, discardLog(), acct, usedGB, time.Now()); err != nil {
		t.Fatalf("EnforceQuota: %v", err)
	}
	got, err := s.AccountByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// TestEnforceQuota_FreeHardStopLiftsUnderQuota — the Free hard stop is per
// month (spec §4.7 quota ladder), but nothing lifted it: a Free account that
// hit its quota once stayed suspended for good, including after upgrading.
func TestEnforceQuota_FreeHardStopLiftsUnderQuota(t *testing.T) {
	ctx := context.Background()
	s := state.NewMemStore()
	acct, err := s.CreateAccount(ctx, "free-stop@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	over := float64(api.PlanFree.PlanIncludedGBHours()) + 1

	if got := enforceQuota(t, ctx, s, acct.ID, over); got.Status != state.AccountSuspended {
		t.Fatalf("over quota: status = %s, want suspended", got.Status)
	}
	// Still over quota: the stop holds.
	if got := enforceQuota(t, ctx, s, acct.ID, over); got.Status != state.AccountSuspended {
		t.Fatalf("still over quota: status = %s, want suspended", got.Status)
	}
	// A new month: usage starts again from zero.
	if got := enforceQuota(t, ctx, s, acct.ID, 0); got.Status != state.AccountActive {
		t.Fatalf("new month: status = %s, want active", got.Status)
	}

	// An upgrade lifts it too, even with the month's usage unchanged.
	if got := enforceQuota(t, ctx, s, acct.ID, over); got.Status != state.AccountSuspended {
		t.Fatalf("over quota again: status = %s, want suspended", got.Status)
	}
	if err := s.UpdateAccountPlan(ctx, acct.ID, api.PlanHobby); err != nil {
		t.Fatal(err)
	}
	if got := enforceQuota(t, ctx, s, acct.ID, over); got.Status != state.AccountActive {
		t.Fatalf("after upgrading to hobby: status = %s, want active", got.Status)
	}
}

// TestEnforceQuota_DoesNotLiftOtherSuspensions — only the quota stop is
// lifted by the quota ladder; an operator suspension stays in place.
func TestEnforceQuota_DoesNotLiftOtherSuspensions(t *testing.T) {
	ctx := context.Background()
	s := state.NewMemStore()
	acct, err := s.CreateAccount(ctx, "operator-held@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateAccountStatus(ctx, acct.ID, state.AccountSuspended); err != nil {
		t.Fatal(err)
	}
	if got := enforceQuota(t, ctx, s, acct.ID, 0); got.Status != state.AccountSuspended {
		t.Fatalf("operator suspension: status = %s, want suspended", got.Status)
	}
}

// TestDunning_RecoveredCustomerGetsAFreshGracePeriod — past_due_at was
// never cleared when a customer paid. MarkDunningStep keeps an existing
// stamp, so their next payment failure reused the old anchor and dunning
// suspended them on its next tick instead of after seven days.
func TestDunning_RecoveredCustomerGetsAFreshGracePeriod(t *testing.T) {
	ctx := context.Background()
	d, s, parker, _, _, clock := dunningTestFixture(t, ctx)
	acct := seedPastDue(t, ctx, s, "recovered@example.com", api.PlanHobby, clock.Add(-30*24*time.Hour))

	// The customer pays: apid's payment_succeeded path.
	if err := s.UpdateAccountStatus(ctx, acct.ID, state.AccountActive); err != nil {
		t.Fatal(err)
	}
	// A later charge fails: apid's payment_failed path.
	if err := s.MarkDunningStep(ctx, acct.ID, state.AccountActive, state.AccountPastDue); err != nil {
		t.Fatal(err)
	}
	got, err := s.AccountByID(ctx, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.PastDueAt == nil || clock.Sub(*got.PastDueAt) > 7*24*time.Hour {
		t.Fatalf("past_due_at = %v; the second failure must start a fresh grace period", got.PastDueAt)
	}
	// Anchor the new failure at the fixture's clock and run the timer.
	backdatePastDueAt(t, s, acct.ID, *clock)
	if err := d.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	parker.mu.Lock()
	parked := len(parker.parked)
	parker.mu.Unlock()
	if got, _ := s.AccountByID(ctx, acct.ID); got.Status != state.AccountPastDue || parked != 0 {
		t.Fatalf("status = %s, parked = %d; a fresh failure must get its 7-day grace", got.Status, parked)
	}
}

// TestDunning_OperatorSuspensionIsNotAdvancedToDeletion — an operator
// suspension of an account that once went past due kept the old
// past_due_at, and dunning advanced it to deleted_pending on its next tick.
func TestDunning_OperatorSuspensionIsNotAdvancedToDeletion(t *testing.T) {
	ctx := context.Background()
	d, s, _, _, _, clock := dunningTestFixture(t, ctx)
	acct := seedPastDue(t, ctx, s, "held@example.com", api.PlanHobby, clock.Add(-60*24*time.Hour))
	if err := s.UpdateAccountStatus(ctx, acct.ID, state.AccountActive); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateAccountStatus(ctx, acct.ID, state.AccountSuspended); err != nil { // operator "suspend"
		t.Fatal(err)
	}
	if err := d.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.AccountByID(ctx, acct.ID); got.Status != state.AccountSuspended {
		t.Fatalf("status = %s; dunning must not advance an operator suspension to deletion", got.Status)
	}
}
