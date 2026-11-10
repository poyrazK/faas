// adr: 20261106103654 — the catalog keeps a branch's expiry from its
// reservation, and the expiry sweep pages past copies it cannot purge yet.
package managedpostgres

import (
	"errors"
	"testing"
	"time"
)

func TestPostgresReserveRecordsABranchExpiry(t *testing.T) {
	store, _, ctx, accountID := postgresStoreFixture(t)
	at := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	source := postgresReadyDatabase(t, store, accountID, "source", at)
	input := postgresTestDatabase(accountID, "pr-1", at.Add(time.Minute))
	input.RestoreSourceDatabaseID, input.RestoreSourceResourceID, input.RestorePointInTime = source.ID, databaseDataResource(source), at
	input.ExpiresAt = input.CreatedAt.Add(time.Hour)
	reserved, created, err := store.Reserve(ctx, input, 10)
	if err != nil || !created || !reserved.ExpiresAt.Equal(input.ExpiresAt) {
		t.Fatalf("Reserve = %+v created=%t, %v; want expires_at %v", reserved, created, err, input.ExpiresAt)
	}
	read, err := store.Get(ctx, accountID, reserved.ID)
	if err != nil || !read.ExpiresAt.Equal(input.ExpiresAt) {
		t.Fatalf("Get = %+v, %v", read, err)
	}
	notRestore := postgresTestDatabase(accountID, "plain", at.Add(time.Minute))
	notRestore.ExpiresAt = notRestore.CreatedAt.Add(time.Hour)
	if _, _, err := store.Reserve(ctx, notRestore, 10); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Reserve of a create with an expiry = %v, want invalid", err)
	}
}

func TestPostgresExpiredBranchPagesSkipPurgesInProgress(t *testing.T) {
	store, pool, ctx, accountID := postgresStoreFixture(t)
	at := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	source := postgresReadyDatabase(t, store, accountID, "source", at)
	var branches []Database
	for i, name := range []string{"pr-1", "pr-2", "pr-3", "pr-later"} {
		input := postgresTestDatabase(accountID, name, at.Add(time.Minute))
		input.RestoreSourceDatabaseID, input.RestoreSourceResourceID, input.RestorePointInTime = source.ID, databaseDataResource(source), at
		input.ExpiresAt = input.CreatedAt.Add(time.Duration(i+1) * time.Hour)
		reserved, _, err := store.Reserve(ctx, input, 10)
		if err != nil {
			t.Fatal(err)
		}
		branches = append(branches, reserved)
	}
	// pr-2 is already being purged; pr-3 has a scheduled delete, which its
	// expiry still purges.
	if _, err := pool.Exec(ctx, `UPDATE managed_postgres_databases SET state='deleting' WHERE id=$1`, branches[1].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE managed_postgres_databases SET state='deleting', purge_after=$2 WHERE id=$1`, branches[2].ID, at.Add(7*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	now := at.Add(time.Minute + 3*time.Hour)
	var got []string
	var after BranchExpiryCursor
	for range 5 {
		page, err := store.ExpiredBranchesPage(ctx, now, after, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		got = append(got, page[0].Name)
		after = BranchExpiryCursor{ExpiresAt: page[0].ExpiresAt, ID: page[0].ID}
	}
	if len(got) != 2 || got[0] != "pr-1" || got[1] != "pr-3" {
		t.Fatalf("expired pages = %v, want [pr-1 pr-3]", got)
	}
	if _, err := store.ExpiredBranchesPage(ctx, now, BranchExpiryCursor{ID: branches[0].ID}, 1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("half a cursor = %v, want invalid", err)
	}
}
