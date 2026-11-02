// adr: 20261031110037 — overage deliveries advance only from the previous
// cumulative charge, and subjects come from the month's allowance events.
package managedpostgres

import (
	"testing"
	"time"
)

func TestPostgresStoreOverageDeliveries(t *testing.T) {
	store, _, ctx, accountID := postgresStoreFixture(t)
	month := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	if subjects, err := store.OverageSubjects(ctx, month); err != nil || len(subjects) != 0 {
		t.Fatalf("subjects before any event: %v, %v", subjects, err)
	}
	for _, threshold := range []string{CapThresholdWarning, CapThresholdAllowance} {
		if _, err := store.RecordCapEvent(ctx, CapEvent{SubjectID: accountID, PeriodStart: month, Threshold: threshold,
			ReachedAt: month.Add(time.Hour), Used: 90000, Allowance: 90000, Ceiling: 180000, Overage: true}); err != nil {
			t.Fatal(err)
		}
	}
	subjects, err := store.OverageSubjects(ctx, month.Add(48*time.Hour))
	if err != nil || len(subjects) != 1 || subjects[0].SubjectID != accountID || subjects[0].Allowance != 90000 || !subjects[0].Overage {
		t.Fatalf("subjects = %+v, %v", subjects, err)
	}
	empty, err := store.OverageDelivered(ctx, "polar", accountID, month)
	if err != nil || empty.Millicents != 0 || !empty.PeriodStart.Equal(month) {
		t.Fatalf("delivered before any step: %+v, %v", empty, err)
	}
	delivery := OverageDelivery{Provider: "polar", SubjectID: accountID, PeriodStart: month, Live: true, BilledUnitSeconds: 3600, Millicents: 20_000, UpdatedAt: month.Add(2 * time.Hour)}
	if recorded, err := store.RecordOverageDelivery(ctx, delivery, OverageDelivery{}); err != nil || !recorded {
		t.Fatalf("first step: %v, %v", recorded, err)
	}
	if recorded, err := store.RecordOverageDelivery(ctx, delivery, OverageDelivery{}); err != nil || recorded {
		t.Fatalf("a replica that lost the race recorded: %v, %v", recorded, err)
	}
	next := delivery
	next.BilledUnitSeconds, next.Millicents, next.UpdatedAt = 7200, 40_000, month.Add(3*time.Hour)
	if recorded, err := store.RecordOverageDelivery(ctx, next, OverageDelivery{Live: true, Millicents: 20_000}); err != nil || !recorded {
		t.Fatalf("second step: %v, %v", recorded, err)
	}
	got, err := store.OverageDelivered(ctx, "polar", accountID, month.Add(time.Hour))
	if err != nil || !got.Live || got.Millicents != 40_000 || got.BilledUnitSeconds != 7200 || !got.UpdatedAt.Equal(next.UpdatedAt) {
		t.Fatalf("delivered = %+v, %v", got, err)
	}
	withStorage := next
	withStorage.Millicents, withStorage.BilledStorageByteSeconds, withStorage.UpdatedAt = 45_000, 1<<40, month.Add(4*time.Hour)
	if recorded, err := store.RecordOverageDelivery(ctx, withStorage, got); err != nil || !recorded {
		t.Fatalf("storage step: %v, %v", recorded, err)
	}
	if got, err = store.OverageDelivered(ctx, "polar", accountID, month); err != nil || got.Millicents != 45_000 || got.BilledStorageByteSeconds != 1<<40 {
		t.Fatalf("delivered with storage = %+v, %v", got, err)
	}
	if other, err := store.OverageDelivered(ctx, "stripe", accountID, month); err != nil || other.Millicents != 0 {
		t.Fatalf("another provider shares the receipt: %+v, %v", other, err)
	}
	stale := next
	stale.Millicents = 60_000
	if recorded, err := store.RecordOverageDelivery(ctx, stale, OverageDelivery{Live: true, Millicents: 40_000}); err != nil || recorded {
		t.Fatalf("a step from a stale previous charge recorded: %v, %v", recorded, err)
	}
	bad := next
	bad.PeriodStart = month.Add(time.Hour)
	bad.Millicents = 80_000
	if _, err := store.RecordOverageDelivery(ctx, bad, OverageDelivery{Live: true, Millicents: 45_000}); err == nil {
		t.Fatal("a mid-month period was accepted")
	}
}

// A subject with overage on, or one already charged in the month, is listed
// even when it never reached the compute allowance: its storage past the
// pool is charged (ADR-20261031110037 amendment 1).
func TestPostgresStoreOverageSubjectsIncludeStorage(t *testing.T) {
	store, _, ctx, accountID := postgresStoreFixture(t)
	month := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	if err := store.SetOverage(ctx, accountID, true, month.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	subjects, err := store.OverageSubjects(ctx, month)
	if err != nil || len(subjects) != 1 || subjects[0].SubjectID != accountID || !subjects[0].Overage || subjects[0].Allowance != 0 {
		t.Fatalf("subjects with overage on = %+v, %v", subjects, err)
	}
	if err := store.SetOverage(ctx, accountID, false, month.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if subjects, err := store.OverageSubjects(ctx, month); err != nil || len(subjects) != 0 {
		t.Fatalf("subjects with overage off and no charge = %+v, %v", subjects, err)
	}
	delivery := OverageDelivery{Provider: "polar", SubjectID: accountID, PeriodStart: month, Live: true, Millicents: 5_000,
		BilledStorageByteSeconds: 1 << 40, UpdatedAt: month.Add(3 * time.Hour)}
	if recorded, err := store.RecordOverageDelivery(ctx, delivery, OverageDelivery{}); err != nil || !recorded {
		t.Fatalf("storage step: %v, %v", recorded, err)
	}
	subjects, err = store.OverageSubjects(ctx, month)
	if err != nil || len(subjects) != 1 || subjects[0].SubjectID != accountID || subjects[0].Overage {
		t.Fatalf("subjects already charged = %+v, %v", subjects, err)
	}
}
