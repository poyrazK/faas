package state_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

func attemptClaim(inv state.Invocation) state.InvocationClaim {
	return state.InvocationClaim{Attempt: inv.Attempts, ReplayGeneration: inv.ReplayGeneration}
}

// adr: 600
func TestInvocationAttemptHistoryRecovery(t *testing.T) {
	forRecipientClaimStores(t, func(t *testing.T, store recipientClaimTestStore, pool *pgxpool.Pool) {
		ctx, account, app, work := seedRecipientClaims(t, store)
		sub := work.RecipientSnapshot[0].ID
		root := state.PublishedEventInvocationID(account, "orders", "evt-three-consumers", sub)
		reader := store.(state.EventReceiptAttemptStore)
		completer := store.(state.InvocationClaimCompletionStore)
		read := func(cursor state.EventReceiptAttemptCursor, limit int) state.EventReceiptAttemptHistory {
			t.Helper()
			history, err := reader.EventReceiptAttempts(ctx, account, "orders", "evt-three-consumers", sub, cursor, limit)
			if err != nil {
				t.Fatal(err)
			}
			return history
		}
		if _, err := store.EnqueueInvocation(ctx, state.Invocation{ID: root, AccountID: account, AppID: app, Source: state.InvocationAsyncInvoke, DueAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
		if len(read(state.EventReceiptAttemptCursor{}, 100).Attempts) != 0 {
			t.Fatal("pending work invented an attempt")
		}
		first := concurrentAttemptClaim(t, ctx, store, root)
		if err := store.FailInvocation(ctx, root, "temporary failure", time.Nanosecond, 3, state.WithInvocationClaim(first)); err != nil {
			t.Fatal(err)
		}
		second, err := store.ClaimInvocationWithCap(ctx, root, "", 60, 10)
		if err != nil {
			t.Fatal(err)
		}
		if err := completer.CompleteInvocationClaim(ctx, root, attemptClaim(first), nil); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("old completion accepted: %v", err)
		}
		if err := store.FailInvocation(ctx, root, "stale failure", 0, 0, state.WithInvocationClaim(first)); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("old failure accepted: %v", err)
		}
		if n, err := store.(interface {
			RequeueExpiredInvocations(context.Context, time.Time, int) (int, error)
		}).RequeueExpiredInvocations(ctx, time.Now().Add(2*time.Minute), 100); err != nil || n != 1 {
			t.Fatalf("recover: %d %v", n, err)
		}
		third, err := store.ClaimInvocationWithCap(ctx, root, "", 60, 10)
		if err != nil {
			t.Fatal(err)
		}
		if err := completer.CompleteInvocationClaim(ctx, root, attemptClaim(second), nil); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("lost lease completed a new attempt")
		}
		if err := store.FailInvocation(ctx, root, "poison record", time.Nanosecond, 3, state.WithInvocationClaim(third)); err != nil {
			t.Fatal(err)
		}
		if _, err := store.RetryQueueDeadLetter(ctx, account, root); err != nil {
			t.Fatal(err)
		}
		replayed, err := store.ClaimInvocationWithCap(ctx, root, "", 60, 10)
		if err != nil {
			t.Fatal(err)
		}
		if replayed.Attempts != 1 || replayed.ReplayGeneration != 1 {
			t.Fatalf("replay claim: %+v", replayed)
		}
		if err := completer.CompleteInvocationClaim(ctx, root, attemptClaim(first), nil); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("old generation completed a reused attempt number")
		}
		if err := store.FailInvocation(ctx, root, "old generation", 0, 0, state.WithInvocationClaim(first)); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("old generation failed a reused attempt number")
		}
		if err := store.FailInvocation(ctx, root, "failed after replay", 0, 0, state.WithInvocationClaim(replayed)); err != nil {
			t.Fatal(err)
		}
		child, err := store.EnqueueInvocation(ctx, state.Invocation{AccountID: account, AppID: app, Source: state.InvocationReplay, ReplayedFromInvocationID: root, DueAt: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		claimedChild, err := store.ClaimInvocationWithCap(ctx, child.ID, "", 60, 10)
		if err != nil {
			t.Fatal(err)
		}
		if err := completer.CompleteInvocationClaim(ctx, child.ID, attemptClaim(claimedChild), nil); err != nil {
			t.Fatal(err)
		}
		history := read(state.EventReceiptAttemptCursor{}, 100)
		want := []string{"succeeded", "failed", "dead_letter", "unknown", "retry"}
		if len(history.Attempts) != len(want) {
			t.Fatalf("history: %+v", history)
		}
		for i, attempt := range history.Attempts {
			if attempt.Outcome != want[i] || attempt.FinishedAt == nil || attempt.FinishedAt.Before(attempt.StartedAt) {
				t.Fatalf("attempt %d: %+v", i, attempt)
			}
		}
		if history.Attempts[4].ErrorDetail != "temporary failure" || history.Attempts[4].NextAttemptAt == nil || history.Attempts[3].ErrorDetail != "dispatch lease expired; requeued" {
			t.Fatalf("failure evidence lost: %+v", history.Attempts)
		}
		page := read(state.EventReceiptAttemptCursor{}, 2)
		if len(page.Attempts) != 2 || page.NextCursor.AfterID == 0 {
			t.Fatal("missing page cursor")
		}
		// A later attempt cannot enter an already traversed older page.
		later, err := store.EnqueueInvocation(ctx, state.Invocation{AccountID: account, AppID: app, Source: state.InvocationReplay, ReplayedFromInvocationID: root, DueAt: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.ClaimInvocation(ctx, later.ID, "", 60); err != nil {
			t.Fatal(err)
		}
		forged, err := store.EnqueueInvocation(ctx, state.Invocation{AccountID: account, AppID: app, Source: state.InvocationReplay, Headers: []byte(`{"x-gregale-event-id":"evt-three-consumers"}`), DueAt: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.ClaimInvocation(ctx, forged.ID, "", 60); err != nil {
			t.Fatal(err)
		}
		older := read(page.NextCursor, 100)
		if len(older.Attempts) != 3 || older.Attempts[0].Outcome != "dead_letter" {
			t.Fatalf("older page: %+v", older)
		}
		for _, probe := range []struct{ account, source, sub string }{{uuid.NewString(), "orders", sub}, {account, "foreign", sub}, {account, "orders", work.RecipientSnapshot[1].ID}, {account, "orders", "foreign"}} {
			h, err := reader.EventReceiptAttempts(ctx, probe.account, probe.source, "evt-three-consumers", probe.sub, state.EventReceiptAttemptCursor{}, 1)
			if probe.sub == work.RecipientSnapshot[1].ID {
				if err != nil || len(h.Attempts) != 0 {
					t.Fatal("sibling evidence leaked")
				}
				continue
			}
			if !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("foreign history: %v", err)
			}
		}
		stale := page.NextCursor
		stale.OutboxID++
		if _, err := reader.EventReceiptAttempts(ctx, account, "orders", "evt-three-consumers", sub, stale, 1); !errors.Is(err, state.ErrConflict) {
			t.Fatal("stale receipt accepted")
		}
		if _, err := store.DeleteInvocationsByIDs(ctx, []string{root}); err != nil {
			t.Fatal(err)
		}
		retained := read(state.EventReceiptAttemptCursor{}, 100)
		if len(retained.Attempts) != 2 || retained.Attempts[0].InvocationID != later.ID || retained.Attempts[1].InvocationID != child.ID {
			t.Fatalf("cascade removed descendant history: %+v", retained)
		}
		pruner := store.(state.InvocationAttemptRetentionStore)
		if n, err := pruner.PruneInvocationAttemptHistory(ctx, time.Now().Add(31*24*time.Hour), 1); err != nil || n != 1 {
			t.Fatalf("prune: %d %v", n, err)
		}
		if n, err := pruner.PruneInvocationAttemptHistory(ctx, time.Now().Add(31*24*time.Hour), 100); err != nil || n != 0 {
			t.Fatalf("pruned running attempt: %d %v", n, err)
		}
		if pool != nil {
			var n int
			if err := pool.QueryRow(ctx, "select count(*) from invocation_attempt_history where outcome='running'").Scan(&n); err != nil || n != 2 {
				t.Fatalf("running retained: %d %v", n, err)
			}
		}
	})
}

func concurrentAttemptClaim(t *testing.T, ctx context.Context, store state.Store, id string) state.Invocation {
	t.Helper()
	var wg sync.WaitGroup
	start := make(chan struct{})
	claims := make(chan state.Invocation, 8)
	failures := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			inv, err := store.ClaimInvocationWithCap(ctx, id, "", 60, 10)
			if err == nil {
				claims <- inv
			} else if !errors.Is(err, state.ErrNotFound) {
				failures <- err
			}
		}()
	}
	close(start)
	wg.Wait()
	close(claims)
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	if len(claims) != 1 {
		t.Fatalf("claims=%d, want one", len(claims))
	}
	return <-claims
}

func TestPgInvocationAttemptHistoryTargetOwnership(t *testing.T) {
	store, pool, _ := pgStoreWithPool(t)
	ctx, account, app, work := seedRecipientClaims(t, store)
	sub := work.RecipientSnapshot[0].ID
	root := state.PublishedEventInvocationID(account, "orders", "evt-three-consumers", sub)
	if _, err := store.EnqueueInvocation(ctx, state.Invocation{ID: root, AccountID: account, AppID: app, Source: state.InvocationAsyncInvoke, DueAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimInvocation(ctx, root, "", 60); err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateAccount(ctx, "attempt-other-owner@example.test", "pro")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "update apps set account_id=$1 where id=$2", other.ID, app); err != nil {
		t.Fatal(err)
	}
	if _, err := store.EventReceiptAttempts(ctx, account, "orders", "evt-three-consumers", sub, state.EventReceiptAttemptCursor{}, 100); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign owner evidence returned: %v", err)
	}
}

// adr: 600
func TestInvocationAttemptHistoryKeyedAndRetention(t *testing.T) {
	forRecipientClaimStores(t, func(t *testing.T, store recipientClaimTestStore, _ *pgxpool.Pool) {
		ctx, account, app, work := seedRecipientClaims(t, store)
		sub := work.RecipientSnapshot[0].ID
		root := state.PublishedEventInvocationID(account, "orders", "evt-three-consumers", sub)
		until := time.Now().Add(time.Minute)
		inv, err := store.EnqueueKeyedInvocation(ctx, state.Invocation{ID: root, AccountID: account, AppID: app, Source: state.InvocationAsyncInvoke, DueAt: time.Now(), ResultRetentionUntil: &until}, workpolicy.Policy{Name: "orders", MaxRunningPerKey: 1}, "s:order")
		if err != nil {
			t.Fatal(err)
		}
		claimed, err := store.ClaimInvocationWithCap(ctx, inv.ID, "", 60, 10)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.(state.InvocationClaimCompletionStore).CompleteInvocationClaim(ctx, inv.ID, attemptClaim(claimed), nil); err != nil {
			t.Fatal(err)
		}
		h, err := store.(state.EventReceiptAttemptStore).EventReceiptAttempts(ctx, account, "orders", "evt-three-consumers", sub, state.EventReceiptAttemptCursor{}, 100)
		if err != nil || len(h.Attempts) != 1 || h.Attempts[0].Outcome != "succeeded" || h.Attempts[0].RetainUntil.Sub(until).Abs() > time.Microsecond {
			t.Fatalf("keyed retained evidence: %+v %v", h, err)
		}
		if n, err := store.(state.InvocationAttemptRetentionStore).PruneInvocationAttemptHistory(ctx, until.Add(time.Second), 1); err != nil || n != 1 {
			t.Fatalf("short retention: %d %v", n, err)
		}
	})
}

// adr: 600
func TestPgInvocationAttemptHistoryAtomicTransition(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	_, account, app, _ := seedRecipientClaims(t, store)
	inv, err := store.EnqueueInvocation(ctx, state.Invocation{AccountID: account, AppID: app, Source: state.InvocationAsyncInvoke, DueAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "update invocations set state='dispatching',attempts=1,lease_expires_at=now()+interval '1 minute' where id=$1", inv.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := tx.QueryRow(ctx, "select count(*) from invocation_attempt_history where invocation_id=$1", inv.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("claim evidence not atomic: %d %v", n, err)
	}
	if _, err := tx.Exec(ctx, "update invocations set state='completed',outcome='success' where id=$1", inv.ID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "select count(*) from invocation_attempt_history where invocation_id=$1", inv.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rollback kept evidence: %d %v", n, err)
	}
	row, err := store.InvocationByID(context.Background(), inv.ID)
	if err != nil || row.State != state.InvocationPending || row.Attempts != 0 {
		t.Fatal("rollback advanced invocation")
	}
}

// The lease must be checked after waiting for the execution-row lock.
func TestPgInvocationAttemptHistoryLeaseExpiresDuringSettlementLockWait(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	_, account, app, _ := seedRecipientClaims(t, store)
	inv, err := store.EnqueueInvocation(ctx, state.Invocation{AccountID: account, AppID: app, Source: state.InvocationAsyncInvoke, DueAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimInvocation(ctx, inv.ID, "", 3)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var blockerPID int
	if err := tx.QueryRow(ctx, "select pg_backend_pid() from invocations where id=$1 for update", inv.ID).Scan(&blockerPID); err != nil {
		t.Fatal(err)
	}
	settled := make(chan error, 1)
	go func() { settled <- store.CompleteInvocationClaim(ctx, inv.ID, attemptClaim(claimed), nil) }()
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, "select exists(select 1 from pg_stat_activity where $1=any(pg_blocking_pids(pid)))", blockerPID).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Until(*claimed.LeaseExpiresAt) < time.Second {
			t.Fatal("settlement did not wait on the live lease")
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(time.Until(*claimed.LeaseExpiresAt) + 50*time.Millisecond)
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-settled; !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("expired lease settled after lock wait: %v", err)
	}
	row, err := store.InvocationByID(ctx, inv.ID)
	if err != nil || row.State != state.InvocationDispatching {
		t.Fatalf("expired settlement changed execution: %+v %v", row, err)
	}
	var outcome string
	if err := pool.QueryRow(ctx, "select outcome from invocation_attempt_history where invocation_id=$1", inv.ID).Scan(&outcome); err != nil || outcome != "running" {
		t.Fatalf("expired settlement changed evidence: %s %v", outcome, err)
	}
}
