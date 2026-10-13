//go:build !no_pg

package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func pgCompleteRetentionExecution(t *testing.T, store *state.PgStore, ctx context.Context, accountID string, admittedAt time.Time) state.Execution {
	t.Helper()
	created, err := store.CreateExecution(ctx, pgExecutionParams(t, accountID, admittedAt, 0, "sealed-retention"))
	if err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
	claim, err := store.ClaimExecution(ctx, "schedd-retention", admittedAt.Add(time.Millisecond), time.Second)
	if err != nil {
		t.Fatalf("ClaimExecution: %v", err)
	}
	if _, err := store.MarkExecutionRunning(ctx, created.ID, *claim.LeaseToken, admittedAt.Add(2*time.Millisecond)); err != nil {
		t.Fatalf("MarkExecutionRunning: %v", err)
	}
	completed, err := store.CompleteExecution(ctx, state.CompleteExecutionParams{
		ID: created.ID, LeaseToken: *claim.LeaseToken, Status: api.ExecutionStatusSucceeded,
		Result: []byte(`{"value":42}`), Stdout: "out\n",
		Usage:      api.ExecutionUsage{WallTimeMS: 40, CPUTimeMS: 30, PeakMemoryMB: 20},
		FinishedAt: admittedAt.Add(3 * time.Millisecond),
	})
	if err != nil {
		t.Fatalf("CompleteExecution: %v", err)
	}
	return completed
}

func pgCountRows(t *testing.T, pool *pgxpool.Pool, ctx context.Context, query, id string) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx, query, id).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return count
}

func TestPgStorePurgeExpiredExecutionsKeepsUsageLedger(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	account := pgExecutionAccount(t, store, ctx, "retention")
	old := time.Now().UTC().Add(-10 * 24 * time.Hour).Truncate(time.Millisecond)
	fresh := time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond)
	expired := pgCompleteRetentionExecution(t, store, ctx, account.ID, old)
	kept := pgCompleteRetentionExecution(t, store, ctx, account.ID, fresh)

	const eventsQuery = `select count(*) from execution_events where execution_id=$1::uuid`
	const ledgerQuery = `select count(*) from execution_usage_ledger where execution_id=$1::uuid`
	if pgCountRows(t, pool, ctx, eventsQuery, expired.ID) == 0 {
		t.Fatal("completion wrote no replay events; the cascade assertion would be vacuous")
	}

	cutoff := time.Now().UTC().Add(-api.ExecutionReceiptRetention)
	deleted, err := store.PurgeExpiredExecutions(ctx, cutoff, 100)
	if err != nil {
		t.Fatalf("PurgeExpiredExecutions: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}
	if _, err := store.ExecutionByID(ctx, account.ID, expired.ID); err == nil {
		t.Fatal("expired receipt is still readable")
	}
	if got := pgCountRows(t, pool, ctx, eventsQuery, expired.ID); got != 0 {
		t.Fatalf("expired replay events = %d, want 0", got)
	}
	if got := pgCountRows(t, pool, ctx, ledgerQuery, expired.ID); got != 1 {
		t.Fatalf("usage ledger rows after purge = %d, want 1", got)
	}
	if _, err := store.ExecutionByID(ctx, account.ID, kept.ID); err != nil {
		t.Fatalf("fresh receipt was purged: %v", err)
	}
	usage, err := store.ExecutionUsageByAccount(ctx, account.ID, old)
	if err != nil {
		t.Fatalf("ExecutionUsageByAccount: %v", err)
	}
	if usage.Runs < 1 || usage.WallTimeMS < 40 {
		t.Fatalf("usage lost by retention: %+v", usage)
	}
}
