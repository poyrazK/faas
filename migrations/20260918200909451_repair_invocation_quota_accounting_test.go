//go:build !no_pg

package migrations_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

const repairInvocationQuotaAccountingVersion int64 = 20260918200909451

func TestMigrationRepairInvocationQuotaAccounting(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	migrateUpOnce(ctx, t, pool)

	accountID := seedAccount(t, ctx, pool)
	appID := seedApp(t, ctx, pool, accountID)
	if _, err := pool.Exec(ctx, `
		insert into account_async_quota (account_id, max_inflight, current_inflight)
		values ($1, 100, 15438)`, accountID); err != nil {
		t.Fatalf("seed corrupt quota: %v", err)
	}
	for _, state := range []string{"dispatching", "pending", "completed"} {
		if _, err := pool.Exec(ctx, `
			insert into invocations (app_id, account_id, source, state, quota_reserved)
			values ($1, $2, 'async_invoke', $3, false)`, appID, accountID, state); err != nil {
			t.Fatalf("seed %s invocation: %v", state, err)
		}
	}

	if _, err := pool.Exec(ctx, `delete from goose_db_version where version_id = $1`, repairInvocationQuotaAccountingVersion); err != nil {
		t.Fatalf("remove repair migration ledger row: %v", err)
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("reapply repair migration: %v", err)
	}

	var current, reservedDispatching, reservedOther int
	if err := pool.QueryRow(ctx, `
		select
			(select current_inflight from account_async_quota where account_id = $1),
			count(*) filter (where state = 'dispatching' and quota_reserved),
			count(*) filter (where state <> 'dispatching' and quota_reserved)
		  from invocations
		 where account_id = $1`, accountID).Scan(&current, &reservedDispatching, &reservedOther); err != nil {
		t.Fatalf("inspect repaired quota: %v", err)
	}
	if current != 1 || reservedDispatching != 1 || reservedOther != 0 {
		t.Fatalf("repair = current %d dispatching-reserved %d other-reserved %d; want 1/1/0", current, reservedDispatching, reservedOther)
	}
}
