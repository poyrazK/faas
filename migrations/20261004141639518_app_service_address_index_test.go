//go:build !no_pg

// adr: 568

package migrations_test

import (
	"context"
	"io/fs"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

const serviceAddressMigrationVersion int64 = 20261004141639518

// migrationVersionBefore returns the newest embedded migration older than
// version. migrateUpTo must land on a real ledger version, and a hard-coded
// predecessor would go stale when a migration is merged in between.
func migrationVersionBefore(t *testing.T, version int64) int64 {
	t.Helper()
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	var prev int64
	for _, entry := range entries {
		prefix, _, ok := strings.Cut(entry.Name(), "_")
		if !ok || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		v, err := strconv.ParseInt(prefix, 10, 64)
		if err == nil && v < version && v > prev {
			prev = v
		}
	}
	if prev == 0 {
		t.Fatalf("no embedded migration precedes %d", version)
	}
	return prev
}

func seedServiceAddressApp(t *testing.T, ctx context.Context, pool *pgxpool.Pool, accountID, status string, createdAt time.Time) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(ctx, `
		insert into apps (account_id, slug, type, ram_mb, max_concurrency,
		                  idle_timeout_s, status, created_at)
		values ($1, $2, 'function', 128, 1, 30, $3, $4)
		returning id::text`, accountID, "svc-addr-"+slugFragment(), status, createdAt).Scan(&id); err != nil {
		t.Fatalf("seed apps row: %v", err)
	}
	return id
}

func serviceAddressIndex(t *testing.T, ctx context.Context, pool *pgxpool.Pool, appID string) *int {
	t.Helper()
	var index *int
	if err := pool.QueryRow(ctx, `select service_address_index from apps where id = $1`, appID).Scan(&index); err != nil {
		t.Fatalf("read service_address_index for %s: %v", appID, err)
	}
	return index
}

func wantServiceAddressIndex(t *testing.T, ctx context.Context, pool *pgxpool.Pool, appID string, want int) {
	t.Helper()
	got := serviceAddressIndex(t, ctx, pool, appID)
	if got == nil || *got != want {
		t.Fatalf("app %s service_address_index = %v, want %d", appID, got, want)
	}
}

// Existing apps are backfilled per account in creation order, tombstones
// included, and the cursor resumes after the backfilled maximum.
func TestMigrationAppServiceAddressIndexBackfill(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	migrateUpTo(t, ctx, pool, migrationVersionBefore(t, serviceAddressMigrationVersion))

	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	accountA := seedAccount(t, ctx, pool)
	accountB := seedAccount(t, ctx, pool)
	a2 := seedServiceAddressApp(t, ctx, pool, accountA, "active", base.Add(2*time.Hour))
	a1 := seedServiceAddressApp(t, ctx, pool, accountA, "deleted", base.Add(time.Hour))
	a3 := seedServiceAddressApp(t, ctx, pool, accountA, "evicted_cold", base.Add(3*time.Hour))
	b1 := seedServiceAddressApp(t, ctx, pool, accountB, "active", base)

	migrateUpTo(t, ctx, pool, serviceAddressMigrationVersion)

	wantServiceAddressIndex(t, ctx, pool, a1, 1)
	wantServiceAddressIndex(t, ctx, pool, a2, 2)
	wantServiceAddressIndex(t, ctx, pool, a3, 3)
	wantServiceAddressIndex(t, ctx, pool, b1, 1)

	a4 := seedServiceAddressApp(t, ctx, pool, accountA, "active", time.Now())
	wantServiceAddressIndex(t, ctx, pool, a4, 4)
	b2 := seedServiceAddressApp(t, ctx, pool, accountB, "active", time.Now())
	wantServiceAddressIndex(t, ctx, pool, b2, 2)
}

// The cursor never hands out a tombstone's index; once the range is used up,
// only tombstones past the 24 h quarantine are reclaimed, and a restored
// tombstone that lost its index is given a fresh one.
func TestMigrationAppServiceAddressIndexReuse(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	migrateUpTo(t, ctx, pool, serviceAddressMigrationVersion)

	account := seedAccount(t, ctx, pool)
	old := seedServiceAddressApp(t, ctx, pool, account, "active", time.Now())
	recent := seedServiceAddressApp(t, ctx, pool, account, "active", time.Now())
	live := seedServiceAddressApp(t, ctx, pool, account, "active", time.Now())
	wantServiceAddressIndex(t, ctx, pool, old, 1)
	wantServiceAddressIndex(t, ctx, pool, recent, 2)
	wantServiceAddressIndex(t, ctx, pool, live, 3)

	if _, err := pool.Exec(ctx, `
		update apps set status = 'deleted', deleted_at = now() - interval '25 hours' where id = $1`, old); err != nil {
		t.Fatalf("delete old app: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		update apps set status = 'deleted', deleted_at = now() - interval '1 hour' where id = $1`, recent); err != nil {
		t.Fatalf("delete recent app: %v", err)
	}
	wantServiceAddressIndex(t, ctx, pool, old, 1)
	wantServiceAddressIndex(t, ctx, pool, recent, 2)

	// Before exhaustion the cursor moves on; tombstones keep their indices.
	fresh := seedServiceAddressApp(t, ctx, pool, account, "active", time.Now())
	wantServiceAddressIndex(t, ctx, pool, fresh, 4)

	if _, err := pool.Exec(ctx, `
		update app_service_address_cursors set last_index = 65534 where account_id = $1`, account); err != nil {
		t.Fatalf("exhaust cursor: %v", err)
	}
	reclaimer := seedServiceAddressApp(t, ctx, pool, account, "active", time.Now())
	wantServiceAddressIndex(t, ctx, pool, reclaimer, 1)
	if got := serviceAddressIndex(t, ctx, pool, old); got != nil {
		t.Fatalf("reclaimed tombstone kept service_address_index %d", *got)
	}
	wantServiceAddressIndex(t, ctx, pool, recent, 2)

	next := seedServiceAddressApp(t, ctx, pool, account, "active", time.Now())
	wantServiceAddressIndex(t, ctx, pool, next, 5)

	// Restoring the tombstone whose index was reclaimed assigns a fresh one;
	// restoring the quarantined tombstone keeps its original index.
	if _, err := pool.Exec(ctx, `update apps set status = 'active', deleted_at = null where id = $1`, old); err != nil {
		t.Fatalf("restore old app: %v", err)
	}
	wantServiceAddressIndex(t, ctx, pool, old, 6)
	if _, err := pool.Exec(ctx, `update apps set status = 'active', deleted_at = null where id = $1`, recent); err != nil {
		t.Fatalf("restore recent app: %v", err)
	}
	wantServiceAddressIndex(t, ctx, pool, recent, 2)
}

// The schema rejects an out-of-range index and a duplicate within one
// account, while two accounts may hold the same index.
func TestMigrationAppServiceAddressIndexConstraints(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	migrateUpTo(t, ctx, pool, serviceAddressMigrationVersion)

	accountA := seedAccount(t, ctx, pool)
	accountB := seedAccount(t, ctx, pool)
	a1 := seedServiceAddressApp(t, ctx, pool, accountA, "active", time.Now())
	a2 := seedServiceAddressApp(t, ctx, pool, accountA, "active", time.Now())
	b1 := seedServiceAddressApp(t, ctx, pool, accountB, "active", time.Now())
	wantServiceAddressIndex(t, ctx, pool, a1, 1)
	wantServiceAddressIndex(t, ctx, pool, b1, 1)

	if _, err := pool.Exec(ctx, `update apps set service_address_index = 1 where id = $1`, a2); err == nil {
		t.Fatal("duplicate service_address_index within one account was accepted")
	}
	if _, err := pool.Exec(ctx, `update apps set service_address_index = 65535 where id = $1`, a2); err == nil {
		t.Fatal("service_address_index 65535 was accepted")
	}

	// An explicit index advances the cursor so it is never handed out again.
	var explicit string
	if err := pool.QueryRow(ctx, `
		insert into apps (account_id, slug, type, ram_mb, max_concurrency, idle_timeout_s, status, service_address_index)
		values ($1, $2, 'function', 128, 1, 30, 'active', 40)
		returning id::text`, accountA, "svc-addr-"+slugFragment()).Scan(&explicit); err != nil {
		t.Fatalf("insert explicit index: %v", err)
	}
	after := seedServiceAddressApp(t, ctx, pool, accountA, "active", time.Now())
	wantServiceAddressIndex(t, ctx, pool, after, 41)
}
