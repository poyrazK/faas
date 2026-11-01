// adr: 20261101143527 — a managed PostgreSQL overage step is reserved before
// it is sent, so a step sent but not recorded is sent again unchanged.
package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"
)

// completeFailingStore fails CompleteOverage while fail is set: the provider
// accepted the step, recording it did not.
type completeFailingStore struct {
	*overageUsageStore
	fail error
}

func (s *completeFailingStore) CompleteOverage(ctx context.Context, pending OveragePendingStep) (bool, error) {
	if s.fail != nil {
		return false, s.fail
	}
	return s.overageUsageStore.CompleteOverage(ctx, pending)
}

func TestOverageBillerResendsAStepThatWasSentButNotRecorded(t *testing.T) {
	ctx := context.Background()
	november := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	f := newOverageFixture(t, november.Add(10*24*time.Hour))
	f.reachAllowance(t, "acct", november, true)
	f.store.used["acct2026-11"] = testAllowance + 3600
	store := &completeFailingStore{overageUsageStore: f.store, fail: errors.New("database down")}
	var published []OverageChargeStep
	newBiller := func() *OverageBiller {
		biller, err := NewOverageBiller(store, OverageBillerOptions{Provider: "polar", EffectiveFrom: november,
			Now:       func() time.Time { return f.now },
			Allowance: func(context.Context, string) (CapLimits, error) { return f.limits, nil },
			Publish: func(_ context.Context, step OverageChargeStep) error {
				published = append(published, step)
				return nil
			}})
		if err != nil {
			t.Fatal(err)
		}
		return biller
	}
	if _, err := newBiller().Deliver(ctx); err == nil || len(published) != 1 || published[0].TargetMillicents != 20_000 {
		t.Fatalf("first pass published %+v, %v", published, err)
	}
	// Usage grows before the next pass. Before reservations, the next pass
	// sent a new step from 0 to 40,000 on top of the 0 to 20,000 the
	// provider already had: 60,000 charged for 40,000 of usage.
	f.store.used["acct2026-11"] = testAllowance + 2*3600
	f.now = f.now.Add(time.Hour)
	store.fail = nil
	if _, err := newBiller().Deliver(ctx); err != nil || len(published) != 2 || published[1] != published[0] {
		t.Fatalf("second pass published %+v, %v; want the reserved step again, unchanged", published, err)
	}
	if delivery, _ := f.store.OverageDelivered(ctx, "polar", "acct", november); delivery.Millicents != 20_000 {
		t.Fatalf("recorded %+v, want the resent step's 20,000", delivery)
	}
	if _, found, _ := f.store.OveragePending(ctx, "polar", "acct", november); found {
		t.Fatal("the completed step is still reserved")
	}
	// The growth is charged from what was recorded.
	if _, err := newBiller().Deliver(ctx); err != nil || len(published) != 3 ||
		published[2].DeliveredMillicents != 20_000 || published[2].TargetMillicents != 40_000 {
		t.Fatalf("third pass published %+v, %v", published, err)
	}
}

func TestOverageBillerLeavesAReservedStepToItsReplica(t *testing.T) {
	ctx := context.Background()
	november := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	f := newOverageFixture(t, november.Add(10*24*time.Hour))
	f.reachAllowance(t, "acct", november, true)
	f.store.used["acct2026-11"] = testAllowance + 3600
	held := OveragePendingStep{Provider: "polar", Live: true, Step: OverageChargeStep{SubjectID: "acct", PeriodStart: november,
		At: f.now, UsedUnitSeconds: testAllowance + 3600, AllowanceSeconds: testAllowance, BilledUnitSeconds: 3600, TargetMillicents: 20_000}}
	if reserved, err := f.store.ReserveOverage(ctx, held); err != nil || !reserved {
		t.Fatalf("reserve = %v, %v", reserved, err)
	}
	if reserved, err := f.store.ReserveOverage(ctx, held); err != nil || reserved {
		t.Fatalf("second reservation = %v, %v; want refused", reserved, err)
	}
	// A pass finishes the reserved step rather than computing its own.
	f.store.used["acct2026-11"] = testAllowance + 2*3600
	if _, err := f.biller(t, true, november).Deliver(ctx); err != nil || len(f.published) != 1 || f.published[0] != held.Step {
		t.Fatalf("published %+v, %v; want the reserved step", f.published, err)
	}
}
