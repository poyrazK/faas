package managedpostgres

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestRestoreSlotSweepPassesDeletingFirstBatch(t *testing.T) {
	f := newSlotFixture(t)
	_, copy := f.sourceAndCopy(t)
	*f.clock = copy.RestoreSlotUntil.Add(time.Minute)
	// Every earlier holder is waiting for purge, and its owner still has no
	// room. They must not hide the 51st copy's expiry or its notice.
	for i := range 50 {
		held := copy
		held.ID, held.AccountID = fmt.Sprintf("blocked-%02d", i), fmt.Sprintf("account-%02d", i)
		held.State, held.PurgeAfter = StateDeleting, f.clock.Add(7*24*time.Hour)
		held.RestoreSlotUntil = copy.RestoreSlotUntil.Add(-time.Hour)
		f.store.databases[held.ID] = held
		source := held
		source.ID, source.State, source.RestoreSlotUntil = fmt.Sprintf("source-%02d", i), StateReady, time.Time{}
		source.PurgeAfter = time.Time{}
		f.store.databases[source.ID] = source
	}
	summary, err := f.sweeper.Sweep(context.Background())
	if err != nil || summary.Holders != 51 || summary.Expired != 1 || summary.Failed != 0 || len(*f.expired) != 1 {
		t.Fatalf("paginated expiry: %+v, %v; expired=%d", summary, err, len(*f.expired))
	}
	current, err := f.service.Get(context.Background(), copy.AccountID, copy.ID)
	if err != nil || !current.Restorable(*f.clock) || !f.provider.held[copy.ProviderResourceID] {
		t.Fatalf("last-page copy was not held: %+v, %v", current, err)
	}
}
