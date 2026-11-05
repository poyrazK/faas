//go:build !no_pg

// adr: 20261022105723
package state_test

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgQueueBacklogBytesCap(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testQueueBacklogBytesCap(t, store)
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
		if err := pool.QueryRow(ctx, `SELECT count(*), coalesce(sum(CASE WHEN source='queue' THEN octet_length(payload::text) ELSE 0 END),0)
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
	check("enqueue")
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
