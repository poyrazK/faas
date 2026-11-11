package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"
)

func sweepGraceFixture(t *testing.T, f graceFixture) {
	t.Helper()
	reconciler, err := NewReconciler(f.service, ReconcilerOptions{Now: f.service.now})
	if err != nil {
		t.Fatal(err)
	}
	if summary, err := reconciler.Sweep(context.Background()); err != nil || summary.Discovered != 1 || summary.Failed != 0 {
		t.Fatalf("hold recovery: %+v, %v", summary, err)
	}
}

func TestScheduledDeleteRecoversUncertainHold(t *testing.T) {
	for _, applied := range []bool{false, true} {
		t.Run(map[bool]string{false: "request failed", true: "confirmation lost"}[applied], func(t *testing.T) {
			provider := newHoldingProvider()
			f := newGraceFixture(t, provider)
			database := f.readyDatabase(t, "orders")
			provider.holdErr, provider.applyHoldBeforeError = ErrUnavailable, applied
			ctx := context.Background()
			if _, err := f.service.ScheduleDelete(ctx, database.AccountID, database.ID); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("uncertain hold: %v", err)
			}
			pending, err := f.service.Get(ctx, database.AccountID, database.ID)
			if err != nil || !pending.Restorable(*f.clock) || pending.LeaseToken != "" || pending.LastErrorCode != deletionHoldPendingCode || !pending.RetryAt.Equal(pending.PurgeAfter) {
				t.Fatalf("uncertain hold lost its recoverable intent: %+v, %v", pending, err)
			}
			provider.holdErr = nil
			*f.clock = pending.UpdatedAt.Add(defaultPollInterval)
			f.service.provisioningEnabled = func() bool { return false }
			f.service.operationsEnabled = func() bool { return false }
			sweepGraceFixture(t, f)
			held, err := f.service.Get(ctx, database.AccountID, database.ID)
			if err != nil || !held.Restorable(*f.clock) || !held.RetryAt.Equal(pending.PurgeAfter) ||
				!provider.held[database.ProviderResourceID] || provider.deleteCalls != 0 {
				t.Fatalf("recovered hold: %+v, %v; held=%v deletes=%d", held, err, provider.held, provider.deleteCalls)
			}
			if recovered, err := f.service.Undelete(ctx, database.AccountID, database.ID); err != nil || recovered.State != StateReady || provider.held[database.ProviderResourceID] {
				t.Fatalf("recover after uncertain hold: %+v, %v", recovered, err)
			}
		})
	}
}

func TestScheduledDeleteRecoversCrashBeforeProviderHold(t *testing.T) {
	provider := newHoldingProvider()
	f := newGraceFixture(t, provider)
	database := f.readyDatabase(t, "orders")
	now := *f.clock
	claimed, err := f.store.ClaimScheduledDelete(context.Background(), database.AccountID, database.ID, "crashed-worker",
		now, now.Add(f.service.leaseDuration), now.Add(7*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	*f.clock = claimed.LeaseUntil
	sweepGraceFixture(t, f)
	current, err := f.service.Get(context.Background(), database.AccountID, database.ID)
	if err != nil || !current.Restorable(*f.clock) || !current.RetryAt.Equal(claimed.PurgeAfter) ||
		!provider.held[database.ProviderResourceID] || provider.deleteCalls != 0 {
		t.Fatalf("crashed hold recovery: %+v, %v; held=%v deletes=%d", current, err, provider.held, provider.deleteCalls)
	}
}

func TestScheduledDeleteReholdsInterruptedUndelete(t *testing.T) {
	provider := newHoldingProvider()
	f := newGraceFixture(t, provider)
	database := f.readyDatabase(t, "orders")
	ctx := context.Background()
	if _, err := f.service.ScheduleDelete(ctx, database.AccountID, database.ID); err != nil {
		t.Fatal(err)
	}
	now := *f.clock
	claimed, err := f.store.ClaimUndelete(ctx, database.AccountID, database.ID, "crashed-undo", now, now.Add(f.service.leaseDuration))
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.ReleaseDeletionHold(ctx, database.ProviderResourceID); err != nil {
		t.Fatal(err)
	}
	// The provider enabled it, but the worker died before committing ready.
	*f.clock = claimed.LeaseUntil
	sweepGraceFixture(t, f)
	if !provider.held[database.ProviderResourceID] || provider.deleteCalls != 0 {
		t.Fatalf("interrupted undo was not re-held: held=%v deletes=%d", provider.held, provider.deleteCalls)
	}
	if recovered, err := f.service.Undelete(ctx, database.AccountID, database.ID); err != nil || recovered.State != StateReady {
		t.Fatalf("retry interrupted undo: %+v, %v", recovered, err)
	}
}

func TestPendingDeletionHoldFailuresDoNotStarveLaterHolds(t *testing.T) {
	provider := newHoldingProvider()
	f := newGraceFixture(t, provider)
	first, second := f.readyDatabase(t, "first"), f.readyDatabase(t, "second")
	provider.holdErr = ErrUnavailable
	ctx := context.Background()
	if _, err := f.service.ScheduleDelete(ctx, first.AccountID, first.ID); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	*f.clock = f.clock.Add(time.Second)
	if _, err := f.service.ScheduleDelete(ctx, second.AccountID, second.ID); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	*f.clock = f.clock.Add(defaultPollInterval - time.Second)
	sweepGraceFixture(t, f) // only the first hold is due; it fails again
	*f.clock = f.clock.Add(defaultPollInterval)
	due, err := f.store.Due(ctx, false, 1, *f.clock)
	if err != nil || len(due) != 1 || due[0].ID != second.ID {
		t.Fatalf("a failed earlier hold monopolized the batch: %+v, %v", due, err)
	}
}
