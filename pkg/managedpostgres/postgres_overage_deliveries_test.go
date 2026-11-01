// adr: 20261031110037 — overage deliveries advance only from the previous
// cumulative charge, and subjects come from the month's allowance events.
package managedpostgres

import (
	"testing"
	"time"
)

// adr: 20261101143527 — one reserved step per provider, subject and month;
// completing it records the delivery and clears it in one transaction.
func TestPostgresStoreOverageReservations(t *testing.T) {
	store, _, ctx, accountID := postgresStoreFixture(t)
	month := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	pending := OveragePendingStep{Provider: "polar", Live: true, Step: OverageChargeStep{SubjectID: accountID, PeriodStart: month,
		At: month.Add(2 * time.Hour), UsedUnitSeconds: 93600, AllowanceSeconds: 90000, BilledUnitSeconds: 3600, TargetMillicents: 20_000}}
	if _, found, err := store.OveragePending(ctx, "polar", accountID, month); err != nil || found {
		t.Fatalf("pending before any reservation: %v, %v", found, err)
	}
	if reserved, err := store.ReserveOverage(ctx, pending); err != nil || !reserved {
		t.Fatalf("reserve: %v, %v", reserved, err)
	}
	other := pending
	other.Step.TargetMillicents = 40_000
	if reserved, err := store.ReserveOverage(ctx, other); err != nil || reserved {
		t.Fatalf("a second reservation for the month: %v, %v", reserved, err)
	}
	got, found, err := store.OveragePending(ctx, "polar", accountID, month.Add(time.Hour))
	if err != nil || !found || got != pending {
		t.Fatalf("pending = %+v, %v, %v; want %+v", got, found, err, pending)
	}
	if recorded, err := store.CompleteOverage(ctx, pending); err != nil || !recorded {
		t.Fatalf("complete: %v, %v", recorded, err)
	}
	if delivered, err := store.OverageDelivered(ctx, "polar", accountID, month); err != nil || delivered.Millicents != 20_000 || !delivered.Live {
		t.Fatalf("delivered = %+v, %v", delivered, err)
	}
	if _, found, err := store.OveragePending(ctx, "polar", accountID, month); err != nil || found {
		t.Fatalf("pending after completion: %v, %v", found, err)
	}
	// A reservation whose base the month's charge has passed is cleared
	// without recording it.
	if reserved, err := store.ReserveOverage(ctx, pending); err != nil || !reserved {
		t.Fatalf("stale reservation: %v, %v", reserved, err)
	}
	if recorded, err := store.CompleteOverage(ctx, pending); err != nil || recorded {
		t.Fatalf("stale completion recorded: %v, %v", recorded, err)
	}
	if _, found, err := store.OveragePending(ctx, "polar", accountID, month); err != nil || found {
		t.Fatalf("stale reservation kept: %v, %v", found, err)
	}
}

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
	if recorded, err := store.RecordOverageDelivery(ctx, delivery, 0); err != nil || !recorded {
		t.Fatalf("first step: %v, %v", recorded, err)
	}
	if recorded, err := store.RecordOverageDelivery(ctx, delivery, 0); err != nil || recorded {
		t.Fatalf("a replica that lost the race recorded: %v, %v", recorded, err)
	}
	next := delivery
	next.BilledUnitSeconds, next.Millicents, next.UpdatedAt = 7200, 40_000, month.Add(3*time.Hour)
	if recorded, err := store.RecordOverageDelivery(ctx, next, 20_000); err != nil || !recorded {
		t.Fatalf("second step: %v, %v", recorded, err)
	}
	got, err := store.OverageDelivered(ctx, "polar", accountID, month.Add(time.Hour))
	if err != nil || !got.Live || got.Millicents != 40_000 || got.BilledUnitSeconds != 7200 || !got.UpdatedAt.Equal(next.UpdatedAt) {
		t.Fatalf("delivered = %+v, %v", got, err)
	}
	if other, err := store.OverageDelivered(ctx, "stripe", accountID, month); err != nil || other.Millicents != 0 {
		t.Fatalf("another provider shares the receipt: %+v, %v", other, err)
	}
	stale := next
	stale.Millicents = 60_000
	if recorded, err := store.RecordOverageDelivery(ctx, stale, 20_000); err != nil || recorded {
		t.Fatalf("a step from a stale previous charge recorded: %v, %v", recorded, err)
	}
	bad := next
	bad.PeriodStart = month.Add(time.Hour)
	bad.Millicents = 80_000
	if _, err := store.RecordOverageDelivery(ctx, bad, 40_000); err == nil {
		t.Fatal("a mid-month period was accepted")
	}
}
