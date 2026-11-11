package managedpostgres

import (
	"testing"
	"time"
)

func TestCostControlOptingOutDoesNotOrphanPendingStorageCharge(t *testing.T) {
	ctx := t.Context()
	month := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	f := newOverageFixture(t, month.Add(20*24*time.Hour))
	f.limits.StorageAllowance = testGiB
	f.limits.StoragePool = 2 * testGiB
	if err := f.store.SetOverage(ctx, "acct", true, f.now); err != nil {
		t.Fatal(err)
	}
	// Storage alone is billable; this subject has no compute-allowance event.
	f.store.used["acct2026-11"] = testAllowance / 2
	f.store.stored["acct2026-11"] = 2 * testGiB * int64(f.now.Sub(month)/time.Second)
	f.lostResponse = true
	if _, err := f.biller(t, true, month).Deliver(ctx); err == nil {
		t.Fatal("wanted an ambiguous provider acknowledgement")
	}
	pending, found, err := f.store.OveragePending(ctx, "polar", "acct", month)
	if err != nil || !found || len(f.published) != 1 {
		t.Fatalf("pending=%+v found=%v published=%v err=%v", pending, found, f.published, err)
	}
	f.now = f.now.Add(time.Minute)
	f.limits.Overage = false
	if err := f.store.SetOverage(ctx, "acct", false, f.now); err != nil {
		t.Fatal(err)
	}
	summary, err := f.biller(t, true, month).Deliver(ctx)
	if err != nil {
		t.Fatal(err)
	}
	after, stillPending, err := f.store.OveragePending(ctx, "polar", "acct", month)
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := f.store.OverageDelivered(ctx, "polar", "acct", month)
	if err != nil {
		t.Fatal(err)
	}
	if stillPending || delivery.Millicents != pending.Step.TargetMillicents {
		t.Errorf("opt-out removed a reserved storage charge from delivery scan: subjects=%d attempts=%d pending=%v target=%d millicents receipt=%d millicents; pending unchanged=%v", summary.Subjects, len(f.published), stillPending, pending.Step.TargetMillicents, delivery.Millicents, after == pending)
	}
}
