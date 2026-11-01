// adr: 20261031123259 — the catalog lets one restore past the owner's limit
// hold the restore slot, refuses a second while it is held, and lists, counts
// and releases slot holders for the sweep.
package managedpostgres

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestPostgresStoreRestoreSlot(t *testing.T) {
	store, _, ctx, accountID := postgresStoreFixture(t)
	at := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	source := postgresReadyDatabase(t, store, accountID, "slot-source", at)
	restoreOf := func(name string) Database {
		input := postgresTestDatabase(accountID, name, at.Add(time.Minute))
		input.RestoreSourceDatabaseID, input.RestoreSourceResourceID, input.RestorePointInTime = source.ID, databaseDataResource(source), at
		return input
	}

	copied, created, err := store.Reserve(ctx, restoreOf("slot-copy"), 1)
	if err != nil || !created || !copied.RestoreSlotUntil.Equal(copied.CreatedAt.Add(api.ManagedPostgresRestoreSlotWindow)) {
		t.Fatalf("restore past the limit = %+v, created %v, %v", copied, created, err)
	}
	if _, _, err := store.Reserve(ctx, restoreOf("slot-second"), 1); !errors.Is(err, ErrRestoreSlotInUse) {
		t.Fatalf("second restore while the slot is held = %v, want ErrRestoreSlotInUse", err)
	}
	if _, _, err := store.Reserve(ctx, postgresTestDatabase(accountID, "slot-create", at), 1); !errors.Is(err, ErrQuotaExceeded) || errors.Is(err, ErrRestoreSlotInUse) {
		t.Fatalf("create past the limit = %v, want the plain quota refusal", err)
	}

	holders, err := store.RestoreSlots(ctx, 100)
	if err != nil || len(holders) != 1 || holders[0].Database.ID != copied.ID || holders[0].SubjectID != accountID {
		t.Fatalf("RestoreSlots = %+v, %v", holders, err)
	}
	if count, err := store.CountSubjectDatabases(ctx, accountID); err != nil || count != 2 {
		t.Fatalf("CountSubjectDatabases = %d, %v, want 2", count, err)
	}
	if cleared, err := store.ClearRestoreSlot(ctx, copied.ID, at); err != nil || !cleared {
		t.Fatalf("ClearRestoreSlot = %v, %v", cleared, err)
	}
	if cleared, err := store.ClearRestoreSlot(ctx, copied.ID, at); err != nil || cleared {
		t.Fatalf("second ClearRestoreSlot = %v, %v, want nothing to clear", cleared, err)
	}
	if holders, err := store.RestoreSlots(ctx, 100); err != nil || len(holders) != 0 {
		t.Fatalf("RestoreSlots after the release = %+v, %v", holders, err)
	}
	// With the slot free again, the next restore past the limit may take it.
	again, created, err := store.Reserve(ctx, restoreOf("slot-again"), 2)
	if err != nil || !created || again.RestoreSlotUntil.IsZero() {
		t.Fatalf("restore after the release = %+v, created %v, %v", again, created, err)
	}
}
