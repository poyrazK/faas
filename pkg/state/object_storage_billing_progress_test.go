// adr: 20261027093220
package state

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestObjectStorageBillingProgressCompareAndSetOnBothStores(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		progress, ok := store.(ObjectStorageBillingProgressStore)
		if !ok {
			t.Fatalf("%T does not keep object storage billing progress", store)
		}
		acct, err := store.CreateAccount(t.Context(), "progress-"+time.Now().Format("150405.000000")+"@example.com", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		period := time.Date(2026, time.October, 1, 0, 0, 0, 0, time.UTC)
		live := ObjectStorageBillingProgress{Provider: "polar", AccountID: acct.ID, PeriodStart: period, Mode: ObjectStorageDeliveryLive}
		if _, err := progress.GetObjectStorageBillingProgress(t.Context(), "polar", acct.ID, period, ObjectStorageDeliveryLive); !errors.Is(err, ErrNotFound) {
			t.Fatalf("progress before any write: %v", err)
		}
		for _, bad := range []ObjectStorageBillingProgress{
			{Provider: "polar", AccountID: acct.ID, PeriodStart: period, Mode: "preactivation"},
			{Provider: "polar", AccountID: acct.ID, PeriodStart: period, Mode: ObjectStorageDeliveryLive, PendingFixed: true},
			{Provider: "polar", AccountID: acct.ID, PeriodStart: period, Mode: ObjectStorageDeliveryLive, DeliveredMillicents: -1},
		} {
			if _, err := progress.SaveObjectStorageBillingProgress(t.Context(), bad); err == nil {
				t.Fatalf("SaveObjectStorageBillingProgress(%+v) accepted an invalid row", bad)
			}
		}

		// Reserve: the first write inserts at version 1.
		live.PendingMillicents = 500
		reserved, err := progress.SaveObjectStorageBillingProgress(t.Context(), live)
		if err != nil || reserved.Version != 1 || reserved.PendingMillicents != 500 || reserved.UpdatedAt.IsZero() {
			t.Fatalf("reserved = %+v, err = %v", reserved, err)
		}
		if _, err := progress.SaveObjectStorageBillingProgress(t.Context(), live); !errors.Is(err, ErrConflict) {
			t.Fatalf("second insert error = %v, want ErrConflict", err)
		}
		// Fix, then complete, each against the version it read.
		fixed := reserved
		fixed.PendingMillicents, fixed.PendingFixed = 300, true
		if fixed, err = progress.SaveObjectStorageBillingProgress(t.Context(), fixed); err != nil || fixed.Version != 2 {
			t.Fatalf("fixed = %+v, err = %v", fixed, err)
		}
		if _, err := progress.SaveObjectStorageBillingProgress(t.Context(), reserved); !errors.Is(err, ErrConflict) {
			t.Fatalf("stale write error = %v, want ErrConflict", err)
		}
		completed := fixed
		completed.DeliveredMillicents, completed.PendingMillicents, completed.PendingFixed = 300, 0, false
		if completed, err = progress.SaveObjectStorageBillingProgress(t.Context(), completed); err != nil || completed.Version != 3 {
			t.Fatalf("completed = %+v, err = %v", completed, err)
		}
		lowered := completed
		lowered.DeliveredMillicents = 200
		if _, err := progress.SaveObjectStorageBillingProgress(t.Context(), lowered); !errors.Is(err, ErrConflict) {
			t.Fatalf("lowering delivered error = %v, want ErrConflict", err)
		}
		got, err := progress.GetObjectStorageBillingProgress(t.Context(), "polar", acct.ID, period, ObjectStorageDeliveryLive)
		if err != nil || got.Version != 3 || got.DeliveredMillicents != 300 || got.PendingMillicents != 0 || got.PendingFixed {
			t.Fatalf("stored progress = %+v, err = %v", got, err)
		}

		// The cap reserves live delivered and pending millicents for the
		// month: not shadow rows, not other months.
		pending := got
		pending.PendingMillicents = 40
		if _, err := progress.SaveObjectStorageBillingProgress(t.Context(), pending); err != nil {
			t.Fatal(err)
		}
		for _, other := range []ObjectStorageBillingProgress{
			{Provider: "polar", AccountID: acct.ID, PeriodStart: period, Mode: ObjectStorageDeliveryShadow, DeliveredMillicents: 7000},
			{Provider: "polar", AccountID: acct.ID, PeriodStart: period.AddDate(0, -1, 0), Mode: ObjectStorageDeliveryLive, DeliveredMillicents: 9000},
		} {
			if _, err := progress.SaveObjectStorageBillingProgress(t.Context(), other); err != nil {
				t.Fatal(err)
			}
		}
		if delivered, pending, err := progress.LiveObjectStorageMillicents(t.Context(), acct.ID, period.Add(36*time.Hour)); err != nil || delivered != 300 || pending != 40 {
			t.Fatalf("live charge = %d delivered, %d pending, err = %v; want 300 and 40", delivered, pending, err)
		}
	})
}
