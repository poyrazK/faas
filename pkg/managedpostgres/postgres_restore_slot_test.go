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

// A reservation that must fit inside the limit (a database switch's copy)
// is refused at the limit instead of taking the slot.
func TestPostgresStoreRestoreWithinTheLimit(t *testing.T) {
	store, _, ctx, accountID := postgresStoreFixture(t)
	at := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	source := postgresReadyDatabase(t, store, accountID, "within-source", at)
	input := postgresTestDatabase(accountID, "within-copy", at.Add(time.Minute))
	input.RestoreSourceDatabaseID, input.RestoreSourceResourceID, input.RestorePointInTime = source.ID, databaseDataResource(source), at

	if _, _, err := store.Reserve(reserveWithinLimit(ctx), input, 1); !errors.Is(err, ErrQuotaExceeded) || errors.Is(err, ErrRestoreSlotInUse) {
		t.Fatalf("restore within a full limit = %v, want the plain quota refusal", err)
	}
	if holders, err := store.RestoreSlots(ctx, 100); err != nil || len(holders) != 0 {
		t.Fatalf("RestoreSlots after the refusal = %+v, %v", holders, err)
	}
	copied, created, err := store.Reserve(reserveWithinLimit(ctx), input, 2)
	if err != nil || !created || !copied.RestoreSlotUntil.IsZero() {
		t.Fatalf("restore with room = %+v, created %v, %v", copied, created, err)
	}
}

func TestPostgresStoreRestoreSlotNotices(t *testing.T) {
	store, _, ctx, accountID := postgresStoreFixture(t)
	at := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	source := postgresReadyDatabase(t, store, accountID, "notice-source", at)
	input := postgresTestDatabase(accountID, "notice-copy", at.Add(time.Minute))
	input.RestoreSourceDatabaseID, input.RestoreSourceResourceID, input.RestorePointInTime = source.ID, databaseDataResource(source), at
	copied, _, err := store.Reserve(ctx, input, 1)
	if err != nil || copied.RestoreSlotUntil.IsZero() {
		t.Fatalf("Reserve = %+v, %v", copied, err)
	}
	until := copied.RestoreSlotUntil
	ending := RestoreSlotNotice{DatabaseID: copied.ID, Kind: RestoreSlotNoticeEnding, SlotUntil: until, RecordedAt: until.Add(-time.Hour)}
	for i, want := range []bool{true, false} {
		if recorded, err := store.RecordRestoreSlotNotice(ctx, ending); err != nil || recorded != want {
			t.Fatalf("record %d = %v, %v, want %v", i, recorded, err, want)
		}
	}
	if _, err := store.RecordRestoreSlotNotice(ctx, RestoreSlotNotice{DatabaseID: copied.ID, Kind: RestoreSlotNoticeExpired, SlotUntil: until, RecordedAt: until}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("an expired notice without a recovery deadline = %v, want ErrInvalid", err)
	}
	now := until.Add(-30 * time.Minute)
	notices, err := store.UnmailedRestoreSlotNotices(ctx, now.Add(-24*time.Hour), now, 10)
	if err != nil || len(notices) != 1 || notices[0].AccountID != accountID || notices[0].DatabaseName != "notice-copy" ||
		!notices[0].SlotUntil.Equal(until) || !notices[0].RestorableUntil.IsZero() {
		t.Fatalf("unmailed = %+v, %v", notices, err)
	}
	// Past the slot's end an 'ending' notice is no longer listed; the
	// expired one is.
	expired := RestoreSlotNotice{DatabaseID: copied.ID, Kind: RestoreSlotNoticeExpired, SlotUntil: until,
		RestorableUntil: until.Add(api.ManagedPostgresDeleteGracePeriod), RecordedAt: until.Add(time.Minute)}
	if recorded, err := store.RecordRestoreSlotNotice(ctx, expired); err != nil || !recorded {
		t.Fatalf("record expired = %v, %v", recorded, err)
	}
	now = until.Add(2 * time.Minute)
	notices, err = store.UnmailedRestoreSlotNotices(ctx, now.Add(-24*time.Hour), now, 10)
	if err != nil || len(notices) != 1 || notices[0].Kind != RestoreSlotNoticeExpired || !notices[0].RestorableUntil.Equal(expired.RestorableUntil) {
		t.Fatalf("unmailed after the slot = %+v, %v", notices, err)
	}
	if notices, err := store.UnmailedRestoreSlotNotices(ctx, now, now, 10); err != nil || len(notices) != 0 {
		t.Fatalf("notices older than since = %+v, %v", notices, err)
	}
}

// ADR-20261102125208 step 3: a suspended database's notice is recorded once,
// listed until mailed, and dropped once the database is back in the plan.
func TestPostgresStorePlanExcessNotices(t *testing.T) {
	store, _, ctx, accountID := postgresStoreFixture(t)
	at := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	kept := postgresReadyDatabase(t, store, accountID, "plan-kept", at)
	over := postgresReadyDatabase(t, store, accountID, "plan-over", at.Add(time.Minute))
	ids := []string{kept.ID, over.ID}
	for i, want := range []int{1, 0} {
		if n, err := store.SyncPlanExcessNotices(ctx, ids, map[string]string{over.ID: PlanExcessDatabaseLimit}, at.Add(time.Hour)); err != nil || n != want {
			t.Fatalf("sync %d = %d, %v; want %d", i, n, err, want)
		}
	}
	notices, err := store.UnmailedPlanExcessNotices(ctx, at, 10)
	if err != nil || len(notices) != 1 || notices[0].DatabaseID != over.ID || notices[0].AccountID != accountID ||
		notices[0].DatabaseName != "plan-over" || notices[0].Reason != PlanExcessDatabaseLimit || !notices[0].RecordedAt.Equal(at.Add(time.Hour)) {
		t.Fatalf("unmailed = %+v, %v", notices, err)
	}
	if notices, err := store.UnmailedPlanExcessNotices(ctx, at.Add(time.Hour), 10); err != nil || len(notices) != 0 {
		t.Fatalf("notices older than since = %+v, %v", notices, err)
	}
	if _, err := store.SyncPlanExcessNotices(ctx, ids, map[string]string{over.ID: "storage"}, at); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown reason = %v; want ErrInvalid", err)
	}
	if _, err := store.SyncPlanExcessNotices(ctx, ids, nil, at.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if notices, err := store.UnmailedPlanExcessNotices(ctx, at, 10); err != nil || len(notices) != 0 {
		t.Fatalf("after lifting = %+v, %v; want none", notices, err)
	}
}

// ADR-20261110184700: the deletion clock carries the date the linked email
// named, and only a notice whose email named none is restarted.
func TestPostgresStorePlanExcessDeletionClock(t *testing.T) {
	store, pool, ctx, accountID := postgresStoreFixture(t)
	at := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	dated := postgresReadyDatabase(t, store, accountID, "plan-dated", at)
	shadowed := postgresReadyDatabase(t, store, accountID, "plan-shadowed", at.Add(time.Minute))
	fresh := postgresReadyDatabase(t, store, accountID, "plan-fresh", at.Add(2*time.Minute))
	ids := []string{dated.ID, shadowed.ID, fresh.ID}
	suspended := map[string]string{dated.ID: PlanExcessDatabaseLimit, shadowed.ID: PlanExcessDatabaseLimit}
	if _, err := store.SyncPlanExcessNotices(ctx, ids, suspended, at); err != nil {
		t.Fatal(err)
	}
	deleteAt := at.Add(api.ManagedPostgresPlanExcessDeleteAfter)
	// As the mail outbox links an email: dated named a date, shadowed none.
	if _, err := pool.Exec(ctx, "UPDATE managed_postgres_plan_excess_notices SET mail_intent_id=gen_random_uuid(),delete_at=$2 WHERE database_id=$1", dated.ID, deleteAt); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "UPDATE managed_postgres_plan_excess_notices SET mail_intent_id=gen_random_uuid() WHERE database_id=$1", shadowed.ID); err != nil {
		t.Fatal(err)
	}
	suspended[fresh.ID] = PlanExcessDatabaseLimit
	if _, err := store.SyncPlanExcessNotices(ctx, ids, suspended, at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	clocks, err := store.PlanExcessSince(ctx, ids)
	if err != nil || len(clocks) != 3 || !clocks[dated.ID].DeleteAt.Equal(deleteAt) || !clocks[shadowed.ID].DeleteAt.IsZero() {
		t.Fatalf("clocks = %+v, %v", clocks, err)
	}
	now := at.Add(2 * time.Hour)
	// fresh is unmailed but inside its window: left for the mail pass.
	if n, err := store.RearmPlanExcessNotices(ctx, ids, now, now.Add(-api.InternalMailManagedPostgresPlanExcessTTL)); err != nil || n != 1 {
		t.Fatalf("rearmed %d, %v; want shadowed only", n, err)
	}
	clocks, _ = store.PlanExcessSince(ctx, ids)
	if !clocks[shadowed.ID].RecordedAt.Equal(now) || !clocks[dated.ID].RecordedAt.Equal(at) || !clocks[fresh.ID].RecordedAt.Equal(at.Add(time.Hour)) {
		t.Fatalf("clocks after rearm = %+v", clocks)
	}
	notices, err := store.UnmailedPlanExcessNotices(ctx, now.Add(-api.InternalMailManagedPostgresPlanExcessTTL), 10)
	if err != nil || len(notices) != 2 {
		t.Fatalf("unmailed after rearm = %+v, %v; want shadowed and fresh", notices, err)
	}
}
