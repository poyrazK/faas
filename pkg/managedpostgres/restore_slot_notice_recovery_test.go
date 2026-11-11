package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type retryRestoreSlotNoticeStore struct {
	*MemoryStore
	fail bool
}

func (s *retryRestoreSlotNoticeStore) RecordRestoreSlotNotice(ctx context.Context, notice RestoreSlotNotice) (bool, error) {
	if s.fail && notice.Kind == RestoreSlotNoticeExpired {
		return false, ErrUnavailable
	}
	return s.MemoryStore.RecordRestoreSlotNotice(ctx, notice)
}

func TestExpiredSlotDeleteRecordsNoticeBeforeProviderIO(t *testing.T) {
	f := newSlotFixture(t)
	ctx := context.Background()
	_, copy := f.sourceAndCopy(t)
	*f.clock = copy.RestoreSlotUntil.Add(time.Minute)
	purgeAfter := f.clock.Add(api.ManagedPostgresDeleteGracePeriod)
	// Stop at the durable claim, as if the worker crashed before the hold.
	if _, err := f.store.ClaimScheduledDelete(ctx, copy.AccountID, copy.ID, "crashed-worker", *f.clock, f.clock.Add(time.Minute), purgeAfter); err != nil {
		t.Fatal(err)
	}
	notices, err := f.store.UnmailedRestoreSlotNotices(ctx, f.clock.Add(-time.Hour), *f.clock, 10)
	if err != nil || len(notices) != 1 || notices[0].Kind != RestoreSlotNoticeExpired || !notices[0].RestorableUntil.Equal(purgeAfter) {
		t.Fatalf("scheduled expiry has no durable email: notices=%+v err=%v", notices, err)
	}
}

func TestRestoreSlotRepairsLegacyExpiryNoticeBeforeReleasingSlot(t *testing.T) {
	f := newSlotFixture(t)
	ctx := context.Background()
	_, copy := f.sourceAndCopy(t)
	*f.clock = copy.RestoreSlotUntil.Add(time.Minute)
	if _, err := f.service.ScheduleDelete(ctx, copy.AccountID, copy.ID); err != nil {
		t.Fatal(err)
	}
	// Simulate a delete committed by the previous release, followed by its
	// failed notice insert. The deletion row is the durable retry evidence.
	f.store.mu.Lock()
	delete(f.store.restoreSlotNotices, memoryRestoreSlotNoticeKey{copy.ID, RestoreSlotNoticeExpired})
	f.store.mu.Unlock()
	notices := &retryRestoreSlotNoticeStore{MemoryStore: f.store, fail: true}
	f.service.store = notices
	*f.limit = 2 // repair must succeed before this holder can be cleared
	if summary, err := f.sweeper.Sweep(ctx); !errors.Is(err, ErrUnavailable) || summary.Failed != 1 || summary.Released != 0 {
		t.Fatalf("failed repair released its only retry evidence: summary=%+v err=%v", summary, err)
	}
	notices.fail = false
	*f.clock = f.clock.Add(time.Minute)
	if summary, err := f.sweeper.Sweep(ctx); err != nil || summary.Released != 1 || summary.Noticed != 1 || len(*f.notified) != 1 {
		t.Fatalf("expiry email did not recover: summary=%+v mailed=%+v err=%v", summary, *f.notified, err)
	}
	if _, err := f.sweeper.Sweep(ctx); err != nil || len(*f.notified) != 1 {
		t.Fatalf("repaired expiry was mailed again: mailed=%+v err=%v", *f.notified, err)
	}
}

func TestCustomerDeleteBeforeSlotEndDoesNotBecomeAnExpiryNotice(t *testing.T) {
	f := newSlotFixture(t)
	ctx := context.Background()
	_, copy := f.sourceAndCopy(t)
	if _, err := f.service.ScheduleDelete(ctx, copy.AccountID, copy.ID); err != nil {
		t.Fatal(err)
	}
	*f.clock = copy.RestoreSlotUntil.Add(time.Minute)
	if _, err := f.sweeper.Sweep(ctx); err != nil || len(*f.notified) != 0 {
		t.Fatalf("customer delete became an automatic expiry: mailed=%+v err=%v", *f.notified, err)
	}
}
