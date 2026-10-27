package state_test

// adr: 20261027093220

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemStoreOrgObjectAdoptionCreditsStorageCharges(t *testing.T) {
	orgObjectAdoptionCreditsStorageCharges(t, state.NewMemStore())
}

// orgObjectAdoptionCreditsStorageCharges: adoption moves the month's storage
// to the organization. What its members were already charged for the month
// becomes the organization's credit, so the month is never billed twice, and
// adoption still succeeds.
func orgObjectAdoptionCreditsStorageCharges(t *testing.T, base state.Store) {
	t.Helper()
	f := setupOffboarding(t, base)
	store := base.(companyObjectStore)
	progress, ok := base.(state.ObjectStorageBillingProgressStore)
	if !ok {
		t.Fatalf("%T does not keep object storage billing progress", base)
	}
	bucket := companyObjectBucket(t, store, f.app, "charged")
	period := state.ObjectStoragePeriod(time.Now())
	member := state.ObjectStorageBillingProgress{Provider: "polar", AccountID: bucket.AccountID, PeriodStart: period, Mode: state.ObjectStorageDeliveryLive, DeliveredMillicents: 300}
	member, err := progress.SaveObjectStorageBillingProgress(t.Context(), member)
	if err != nil {
		t.Fatal(err)
	}
	member.PendingMillicents = 40
	if _, err := progress.SaveObjectStorageBillingProgress(t.Context(), member); err != nil {
		t.Fatal(err)
	}
	for _, other := range []state.ObjectStorageBillingProgress{
		{Provider: "polar", AccountID: bucket.AccountID, PeriodStart: period, Mode: state.ObjectStorageDeliveryShadow, DeliveredMillicents: 50},
		{Provider: "polar", AccountID: bucket.AccountID, PeriodStart: period.AddDate(0, -1, 0), Mode: state.ObjectStorageDeliveryLive, DeliveredMillicents: 9000},
	} {
		if _, err := progress.SaveObjectStorageBillingProgress(t.Context(), other); err != nil {
			t.Fatal(err)
		}
	}

	adoptObjectCompany(t, f)
	org, err := base.OrgByID(t.Context(), f.org.ID)
	if err != nil || !org.ResourceOwnershipEnabled {
		t.Fatalf("adoption = %+v, %v; want resource ownership enabled", org, err)
	}
	for mode, want := range map[string]int64{state.ObjectStorageDeliveryLive: 340, state.ObjectStorageDeliveryShadow: 50} {
		got, err := progress.GetObjectStorageBillingProgress(t.Context(), "polar", f.org.ID, period, mode)
		if err != nil || got.CreditedMillicents != want || got.DeliveredMillicents != 0 || got.PendingMillicents != 0 || got.Version < 1 {
			t.Fatalf("organization %s progress = %+v, err = %v; want %d credited", mode, got, err, want)
		}
	}
	if _, err := progress.GetObjectStorageBillingProgress(t.Context(), "polar", f.org.ID, period.AddDate(0, -1, 0), state.ObjectStorageDeliveryLive); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("a closed month was credited: %v", err)
	}
	// The organization was not billed the credit, so its cap does not
	// reserve it.
	if delivered, pending, err := progress.LiveObjectStorageMillicents(t.Context(), f.org.ID, period); err != nil || delivered != 0 || pending != 0 {
		t.Fatalf("organization charge = %d delivered, %d pending, err = %v; want none", delivered, pending, err)
	}
	// A step keeps the credit it read.
	got, _ := progress.GetObjectStorageBillingProgress(t.Context(), "polar", f.org.ID, period, state.ObjectStorageDeliveryLive)
	got.DeliveredMillicents = 10
	if saved, err := progress.SaveObjectStorageBillingProgress(t.Context(), got); err != nil || saved.CreditedMillicents != 340 {
		t.Fatalf("step after adoption = %+v, err = %v; want credit kept", saved, err)
	}
}
