//go:build !no_pg

// adr: 20261022105723
package state_test

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/pressly/goose/v3"
)

func TestPgQueueBacklogBytesCap(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testQueueBacklogBytesCap(t, store)
}

func TestPgAsyncInvocationDepthCap(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testAsyncInvocationDepthCap(t, store)
}

func TestPgQueueMessageExpiry(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testQueueMessageExpiry(t, store)
}

func TestPgQueueDepthReadsStopAtLimit(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testQueueDepthReadsStopAtLimit(t, store)
}

// The backlog counters admission reads must equal the app's pending and
// dispatching rows, and their queue payload bytes, after every kind of
// change a row goes through.
func TestPgInvocationBacklogCountersTrackEveryChange(t *testing.T) {
	store, ctx, pool := pgWithPool(t)
	app := seedQueueBacklogApp(t, store, seedQueueBacklogAccount(t, store, api.PlanPro))
	check := func(step string) {
		t.Helper()
		var countedPending, countedBytes, pending, bytes int64
		if err := pool.QueryRow(ctx, `SELECT coalesce(sum(pending),0), coalesce(sum(bytes),0) FROM invocation_backlog_counters WHERE app_id=$1`,
			app.ID).Scan(&countedPending, &countedBytes); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `SELECT count(*), coalesce(sum(CASE WHEN source IN ('queue','async_invoke','delayed_task') THEN octet_length(payload::text) ELSE 0 END),0)
			FROM invocations WHERE app_id=$1 AND state IN ('pending','dispatching')`, app.ID).Scan(&pending, &bytes); err != nil {
			t.Fatal(err)
		}
		if countedPending != pending || countedBytes != bytes {
			t.Fatalf("%s: counters hold %d rows / %d bytes, the table %d / %d", step, countedPending, countedBytes, pending, bytes)
		}
	}
	enqueue := func(source state.InvocationSource) state.Invocation {
		t.Helper()
		inv, err := enqueueBacklogMessage(t, store, app, source, 0)
		if err != nil {
			t.Fatal(err)
		}
		return inv
	}
	exec := func(step, sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", step, err)
		}
		check(step)
	}
	claimed, rewritten := enqueue(state.InvocationQueue), enqueue(state.InvocationQueue)
	enqueue(state.InvocationDelayedTask)
	asyncInv := enqueue(state.InvocationAsyncInvoke)
	enqueue(state.InvocationCron)
	check("enqueue")
	exec("async payload rewrite", `UPDATE invocations SET payload='{"message":"an async payload rewritten longer"}' WHERE id=$1`, asyncInv.ID)
	if _, err := store.ClaimInvocationWithCap(ctx, claimed.ID, "", 600, 10); err != nil {
		t.Fatal(err)
	}
	check("claim")
	exec("payload rewrite", `UPDATE invocations SET payload='{"message":"a longer payload than before"}' WHERE id=$1`, rewritten.ID)
	exec("complete", `UPDATE invocations SET state='completed', completed_at=now() WHERE id=$1`, claimed.ID)
	exec("replay", `UPDATE invocations SET state='pending', completed_at=NULL WHERE id=$1`, claimed.ID)
	if err := store.CancelInvocation(ctx, rewritten.ID); err != nil {
		t.Fatal(err)
	}
	check("cancel")
	exec("delete", `DELETE FROM invocations WHERE id=$1`, claimed.ID)
	waiting := enqueue(state.InvocationQueue)
	if n, err := store.ExpirePendingQueueMessages(ctx, waiting.CreatedAt.AddDate(0, 0, 30), 100); err != nil || n != 1 {
		t.Fatalf("expiry = %d, %v; want the waiting message", n, err)
	}
	check("expiry")
}

func pgQueueReplayHooks(pool *pgxpool.Pool) queueReplayHooks {
	return queueReplayHooks{
		backdate: func(t *testing.T, id string, at time.Time) {
			t.Helper()
			if _, err := pool.Exec(t.Context(), `UPDATE invocations SET created_at=$2, due_at=$2 WHERE id=$1`, id, at); err != nil {
				t.Fatal(err)
			}
		},
		fill: func(t *testing.T, app state.App, n int) {
			t.Helper()
			if _, err := pool.Exec(t.Context(), `INSERT INTO invocations (app_id, account_id, source, deployment_scope)
				SELECT $1::uuid, $2::uuid, 'queue', '' FROM generate_series(1, $3::int)`, app.ID, app.AccountID, n); err != nil {
				t.Fatal(err)
			}
		},
	}
}

func TestPgQueueReplayRestartsRetention(t *testing.T) {
	store, _, pool := pgWithPool(t)
	testQueueReplayRestartsRetention(t, store, pgQueueReplayHooks(pool))
}

func TestPgQueueReplayMeetsQueueDepth(t *testing.T) {
	store, _, pool := pgWithPool(t)
	testQueueReplayMeetsQueueDepth(t, store, pgQueueReplayHooks(pool))
}

func TestPgQueueLeaseLapsedOnLastAttemptDeadLetters(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testQueueLeaseLapsedOnLastAttempt(t, store)
}

// Async and delayed rows pending before the async backlog bytes migration
// were counted with 0 bytes. Their decrement under the new function stops at
// zero, so the counters never go below zero or above the table, and they are
// exact again once that backlog drains (ADR-20261022105723 amendment
// 2026-11-13).
func TestPgAsyncBacklogBytesMigrationNeverOvercounts(t *testing.T) {
	store, ctx, pool := pgWithPool(t)
	app := seedQueueBacklogApp(t, store, seedQueueBacklogAccount(t, store, api.PlanPro))
	sqlDB := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { _ = sqlDB.Close() })
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS,
		goose.WithDisableVersioning(true), goose.WithDisableGlobalRegistry(true))
	if err != nil {
		t.Fatal(err)
	}
	const version = 20261113190512417
	if _, err = provider.ApplyVersion(ctx, version, false); err != nil {
		t.Fatalf("down: %v", err)
	}
	var before []state.Invocation
	for _, source := range []state.InvocationSource{state.InvocationAsyncInvoke, state.InvocationDelayedTask, state.InvocationAsyncInvoke} {
		inv, err := enqueueBacklogMessage(t, store, app, source, 0)
		if err != nil {
			t.Fatal(err)
		}
		before = append(before, inv)
	}
	if _, err = provider.ApplyVersion(ctx, version, true); err != nil {
		t.Fatalf("up: %v", err)
	}
	counted := func(when string) (int64, int64) {
		t.Helper()
		var counters, negative, table int64
		if err := pool.QueryRow(ctx, `SELECT coalesce(sum(bytes),0), count(*) FILTER (WHERE bytes < 0) FROM invocation_backlog_counters WHERE app_id=$1`, app.ID).Scan(&counters, &negative); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `SELECT coalesce(sum(octet_length(payload::text)),0) FROM invocations
			WHERE app_id=$1 AND state IN ('pending','dispatching') AND source IN ('queue','async_invoke','delayed_task')`, app.ID).Scan(&table); err != nil {
			t.Fatal(err)
		}
		if negative > 0 || counters > table {
			t.Fatalf("%s: counters hold %d bytes (%d negative slots), pending rows %d", when, counters, negative, table)
		}
		return counters, table
	}
	if counters, _ := counted("after upgrade"); counters != 0 {
		t.Fatalf("after upgrade counters hold %d bytes; rows from before it are not counted", counters)
	}
	var after []state.Invocation
	for range 8 {
		inv, err := enqueueBacklogMessage(t, store, app, state.InvocationAsyncInvoke, 0)
		if err != nil {
			t.Fatal(err)
		}
		after = append(after, inv)
	}
	counted("after new rows")
	for _, inv := range before {
		if err := store.CancelInvocation(ctx, inv.ID); err != nil {
			t.Fatal(err)
		}
		counted("after cancelling a row from before the upgrade")
	}
	for _, inv := range after {
		if err := store.CancelInvocation(ctx, inv.ID); err != nil {
			t.Fatal(err)
		}
	}
	if counters, table := counted("after the backlog drained"); counters != 0 || table != 0 {
		t.Fatalf("drained: counters %d, rows %d", counters, table)
	}
	// Exact again from here.
	if _, err := enqueueBacklogMessage(t, store, app, state.InvocationDelayedTask, 0); err != nil {
		t.Fatal(err)
	}
	if counters, table := counted("after the drain"); counters != table || table == 0 {
		t.Fatalf("after the drain counters %d, rows %d", counters, table)
	}
}

