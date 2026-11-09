package managedpostgres

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPostgresRestoreSlotPagesSurviveEarlierRemoval(t *testing.T) {
	store, pool, ctx, _ := postgresStoreFixture(t)
	at := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	for range 3 {
		account, err := state.NewPgStore(pool).CreateAccount(ctx, uuid.NewString()+"@slot-pages.test", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		source := postgresReadyDatabase(t, store, account.ID, "source", at)
		input := postgresTestDatabase(account.ID, "copy", at.Add(time.Minute))
		input.RestoreSourceDatabaseID, input.RestoreSourceResourceID, input.RestorePointInTime = source.ID, databaseDataResource(source), at
		if _, _, err := store.Reserve(ctx, input, 1); err != nil {
			t.Fatal(err)
		}
	}
	all, err := store.RestoreSlots(ctx, 10)
	if err != nil || len(all) != 3 {
		t.Fatalf("holders: %+v, %v", all, err)
	}
	var after RestoreSlotCursor
	for i := range 3 {
		page, err := store.RestoreSlotsPage(ctx, after, 1)
		if err != nil || len(page) != 1 || page[0].Database.ID != all[i].Database.ID {
			t.Fatalf("page %d: %+v, %v", i, page, err)
		}
		database := page[0].Database
		after = RestoreSlotCursor{Until: database.RestoreSlotUntil, ID: database.ID}
		if _, err := store.ClearRestoreSlot(ctx, database.ID, at); err != nil {
			t.Fatal(err)
		}
	}
	if page, err := store.RestoreSlotsPage(ctx, after, 1); err != nil || len(page) != 0 {
		t.Fatalf("last page: %+v, %v", page, err)
	}
}
