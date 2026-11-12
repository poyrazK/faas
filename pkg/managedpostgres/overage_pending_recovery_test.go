// adr: 20261101143527 — an opt-out cannot hide an ambiguously sent storage step.
package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestStorageOveragePendingSurvivesOptOut(t *testing.T) {
	for _, failure := range []string{"publish response lost", "delivery receipt lost"} {
		t.Run(failure, func(t *testing.T) {
			ctx := context.Background()
			month := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
			f := newOverageFixture(t, month.AddDate(0, 1, 0).Add(-time.Second))
			f.limits.StorageAllowance = testGiB
			if err := f.store.SetOverage(ctx, "acct", true, f.now); err != nil {
				t.Fatal(err)
			}
			f.store.used["acct2026-11"] = testAllowance / 2
			f.store.stored["acct2026-11"] = 2 * testGiB * (testNovemberSecs - 1)
			receipt := &completeFailingStore{overageUsageStore: f.store}
			biller := f.biller(t, true, month)
			biller.billing = receipt
			if failure == "publish response lost" {
				f.lostResponse = true
			} else {
				receipt.fail = errors.New("receipt unavailable")
			}
			if _, err := biller.Deliver(ctx); err == nil || len(f.published) != 1 {
				t.Fatalf("ambiguous delivery setup: steps=%d err=%v", len(f.published), err)
			}
			held, found, err := f.store.OveragePending(ctx, "polar", "acct", month)
			if err != nil || !found || held.Step.BilledStorageByteSeconds == 0 || held.Step.BilledUnitSeconds != 0 {
				t.Fatalf("storage-only pending: %+v %t %v", held, found, err)
			}
			if err := f.store.SetOverage(ctx, "acct", false, f.now); err != nil {
				t.Fatal(err)
			}
			f.limits.Overage = false
			receipt.fail = nil
			// Recover after restart and UTC month rollover. Historical terms
			// may be unavailable; the already-sent step is still immutable.
			f.now = month.AddDate(0, 1, 0).Add(time.Hour)
			biller = f.biller(t, true, month)
			biller.options.Allowance = func(context.Context, string, time.Time) (CapLimits, error) { return CapLimits{}, ErrUnavailable }
			summary, err := biller.Deliver(ctx)
			if err != nil || summary.Delivered != 1 || len(f.published) != 2 || f.published[0] != f.published[1] {
				t.Fatalf("opt-out changed or abandoned the pending step: %+v steps=%+v err=%v", summary, f.published, err)
			}
			delivery, err := f.store.OverageDelivered(ctx, "polar", "acct", month)
			if err != nil || delivery.Millicents != held.Step.TargetMillicents || delivery.BilledStorageByteSeconds != held.Step.BilledStorageByteSeconds {
				t.Fatalf("delivery=%+v err=%v", delivery, err)
			}
			if _, found, err := f.store.OveragePending(ctx, "polar", "acct", month); err != nil || found {
				t.Fatalf("pending after recovery=%t err=%v", found, err)
			}
		})
	}
}

func TestPostgresPendingStorageSubjectsSurviveOptOut(t *testing.T) {
	store, _, ctx, accountID := postgresStoreFixture(t)
	month := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	if err := store.SetOverage(ctx, accountID, true, month.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	held := OveragePendingStep{Provider: "polar", Live: true, Previous: OverageDelivery{Provider: "polar", SubjectID: accountID, PeriodStart: month}, Step: OverageChargeStep{SubjectID: accountID, PeriodStart: month, At: month.Add(29 * 24 * time.Hour), UsedUnitSeconds: testAllowance / 2, AllowanceSeconds: testAllowance, UsedStorageByteSeconds: 2 * testGiB * testNovemberSecs, StoragePoolBytes: testGiB, BilledStorageByteSeconds: testGiB * testNovemberSecs, TargetMillicents: 1000}}
	if reserved, err := store.ReserveOverage(ctx, held); err != nil || !reserved {
		t.Fatalf("reserve %t %v", reserved, err)
	}
	if err := store.SetOverage(ctx, accountID, false, held.Step.At); err != nil {
		t.Fatal(err)
	}
	subjects, err := store.OverageSubjects(ctx, month)
	if err != nil || len(subjects) != 1 || subjects[0].SubjectID != accountID || subjects[0].Overage {
		t.Fatalf("pending-only subject=%+v err=%v", subjects, err)
	}
	if subjects, err := store.OverageSubjects(ctx, month.AddDate(0, 1, 0)); err != nil || len(subjects) != 0 {
		t.Fatalf("pending leaked into another month: %+v %v", subjects, err)
	}
	recovered, found, err := store.OveragePending(ctx, "polar", accountID, month)
	if err != nil || !found || recovered != held {
		t.Fatalf("reserved payload changed: %+v %t %v", recovered, found, err)
	}
	if recorded, err := store.CompleteOverage(ctx, recovered); err != nil || !recorded {
		t.Fatalf("complete %t %v", recorded, err)
	}
}
