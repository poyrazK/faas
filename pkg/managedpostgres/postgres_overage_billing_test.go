// adr: 20261031165104 — the overage billing ledger inserts once, updates only
// the version it read, and keeps a closed month's closing time.
package managedpostgres

import (
	"errors"
	"testing"
	"time"
)

func TestPostgresStoreBillingProgress(t *testing.T) {
	store, _, ctx, accountID := postgresStoreFixture(t)
	october := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	at := october.Add(time.Hour)
	if _, err := store.GetBillingProgress(ctx, "polar", accountID, october, BillingModeShadow); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an empty month = %v, want ErrNotFound", err)
	}
	first, err := store.SaveBillingProgress(ctx, BillingProgress{Provider: "polar", SubjectID: accountID, PeriodStart: october.Add(36 * time.Hour),
		Mode: BillingModeShadow, PendingMillicents: 5_000}, at)
	if err != nil || first.Version != 1 || !first.PeriodStart.Equal(october) || !first.UpdatedAt.Equal(at) {
		t.Fatalf("insert = %+v, %v", first, err)
	}
	if _, err := store.SaveBillingProgress(ctx, BillingProgress{Provider: "polar", SubjectID: accountID, PeriodStart: october, Mode: BillingModeShadow}, at); !errors.Is(err, ErrConflict) {
		t.Fatalf("a second insert = %v, want ErrConflict", err)
	}
	fixed := first
	fixed.PendingFixed = true
	second, err := store.SaveBillingProgress(ctx, fixed, at.Add(time.Minute))
	if err != nil || second.Version != 2 || !second.PendingFixed {
		t.Fatalf("update = %+v, %v", second, err)
	}
	if _, err := store.SaveBillingProgress(ctx, fixed, at.Add(2*time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("a stale version = %v, want ErrConflict", err)
	}
	closedAt := at.Add(time.Hour)
	done := second
	done.DeliveredMillicents, done.PendingMillicents, done.PendingFixed, done.ClosedAt = 5_000, 0, false, &closedAt
	if _, err := store.SaveBillingProgress(ctx, done, closedAt); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetBillingProgress(ctx, "polar", accountID, october, BillingModeShadow)
	if err != nil || got.DeliveredMillicents != 5_000 || got.ClosedAt == nil || !got.ClosedAt.Equal(closedAt) || got.Version != 3 {
		t.Fatalf("closed month = %+v, %v", got, err)
	}
	if _, err := store.GetBillingProgress(ctx, "polar", accountID, october, BillingModeLive); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the live row of a shadow month = %v, want ErrNotFound", err)
	}
}
