package managedpostgres

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestPostgresExpiredSlotDeleteAndNoticeCommitTogether(t *testing.T) {
	store, pool, ctx, accountID := postgresStoreFixture(t)
	at := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	source := postgresReadyDatabase(t, store, accountID, "expiry-source", at)
	input := postgresTestDatabase(accountID, "expiry-copy", at.Add(time.Minute))
	input.RestoreSourceDatabaseID, input.RestoreSourceResourceID, input.RestorePointInTime = source.ID, databaseDataResource(source), at
	copy, _, err := store.Reserve(ctx, input, 1)
	if err != nil {
		t.Fatal(err)
	}
	provisionAt := input.CreatedAt.Add(time.Second)
	claimed, err := store.Claim(ctx, accountID, copy.ID, "provision-copy", StateProvisioning, provisionAt, provisionAt.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordProviderResource(ctx, copy.ID, claimed.LeaseToken, "restored-branch", provisionAt); err != nil {
		t.Fatal(err)
	}
	copy, err = store.FinishProvision(ctx, copy.ID, claimed.LeaseToken, provisionAt)
	if err != nil {
		t.Fatal(err)
	}
	now := copy.RestoreSlotUntil.Add(time.Minute)
	purgeAfter := now.Add(api.ManagedPostgresDeleteGracePeriod)
	// Fail precisely after the schedule UPDATE, at the notice INSERT.
	if _, err := pool.Exec(ctx, `CREATE FUNCTION reject_expiry_notice() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'injected expiry notice write failure'; END $$;
CREATE TRIGGER reject_expiry_notice BEFORE INSERT ON managed_postgres_restore_slot_notices
FOR EACH ROW EXECUTE FUNCTION reject_expiry_notice()`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimScheduledDelete(ctx, accountID, copy.ID, "schedule", now, now.Add(time.Minute), purgeAfter); err == nil {
		t.Fatal("scheduled a delete despite its failed notice write")
	}
	unchanged, err := store.Get(ctx, accountID, copy.ID)
	if err != nil || unchanged.State != StateReady || !unchanged.PurgeAfter.IsZero() || unchanged.LeaseToken != "" {
		t.Fatalf("failed notice left a committed delete: database=%+v err=%v", unchanged, err)
	}
	if _, err := pool.Exec(ctx, `DROP TRIGGER reject_expiry_notice ON managed_postgres_restore_slot_notices`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimScheduledDelete(ctx, accountID, copy.ID, "retry", now, now.Add(time.Minute), purgeAfter); err != nil {
		t.Fatal(err)
	}
	// No sweeper or provider call is needed to retain the email intent.
	notices, err := store.UnmailedRestoreSlotNotices(ctx, now.Add(-time.Hour), now, 10)
	if err != nil || len(notices) != 1 || notices[0].DatabaseID != copy.ID || notices[0].Kind != RestoreSlotNoticeExpired ||
		!notices[0].RestorableUntil.Equal(purgeAfter) {
		t.Fatalf("committed delete has no recoverable notice: notices=%+v err=%v", notices, err)
	}
}
