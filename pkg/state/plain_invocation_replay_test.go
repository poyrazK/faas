package state_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

// adr: 587
func TestMemPlainReplayLifecycle(t *testing.T) {
	store := state.NewMemStore()
	ctx := t.Context()
	acct, err := store.CreateAccount(ctx, "plain-replay@tests.example", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	// Native MemStore IDs are compact UUIDs; ownership still resolves them.
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "plain-replay"})
	if err != nil {
		t.Fatal(err)
	}
	assertPlainReplayLifecycle(t, ctx, store, acct.ID, app.ID)
}

func TestPgPlainReplayLifecycle(t *testing.T) {
	store, ctx, appID, accountID := seedInvocationPg(t)
	assertPlainReplayLifecycle(t, ctx, store, accountID, appID)
}

func failPlainReplay(t *testing.T, ctx context.Context, store state.Store, id string) {
	t.Helper()
	claim, err := store.ClaimInvocationWithCap(ctx, id, "", 30, 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FailInvocation(ctx, id, "handler failure", 0, 0, state.WithClaimAttempt(claim.Attempts)); err != nil {
		t.Fatal(err)
	}
}

func assertPlainReplayLifecycle(t *testing.T, ctx context.Context, store state.Store, accountID, appID string) {
	t.Helper()
	replayer := store.(state.PlainInvocationReplayStore)
	root, err := store.EnqueueInvocation(ctx, state.Invocation{AccountID: accountID, AppID: appID,
		DeploymentScope: "staging", Source: state.InvocationAsyncInvoke, Method: "PUT", Path: "/orders/123",
		Payload: json.RawMessage(`{"order":123}`), Headers: json.RawMessage(`{"x-order":"123"}`), DueAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := replayer.ReplayPlainInvocation(ctx, accountID, root.ID, state.PlainInvocationReplayOptions{}); !errors.Is(err, state.ErrPlainReplayNotAllowed) {
		t.Fatalf("active parent accepted: %v", err)
	}
	failPlainReplay(t, ctx, store, root.ID)
	if _, err := replayer.ExistingPlainInvocationReplay(ctx, accountID, root.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("fabricated existing child: %v", err)
	}
	deadline, retention := time.Now().Add(time.Minute), time.Now().Add(time.Hour)
	opts := state.PlainInvocationReplayOptions{Headers: json.RawMessage(`{"x-replay":"1"}`),
		RetryPolicyJSON: json.RawMessage(`{"max_attempts":3}`), DeadlineAt: &deadline, ResultRetentionUntil: &retention}
	var wg sync.WaitGroup
	start := make(chan struct{})
	results, failures := make(chan state.Invocation, 8), make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			inv, err := replayer.ReplayPlainInvocation(ctx, accountID, root.ID, opts)
			if err != nil {
				failures <- err
				return
			}
			results <- inv
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		t.Fatalf("concurrent replay: %v", err)
	}
	var child state.Invocation
	for inv := range results {
		if child.ID != "" && child.ID != inv.ID {
			t.Fatal("concurrent recovery created multiple children")
		}
		child = inv
	}
	if child.ID == "" || child.ID == root.ID || child.Source != state.InvocationReplay || child.State != state.InvocationPending ||
		child.DeploymentScope != root.DeploymentScope || child.ReplayedFromInvocationID != root.ID || child.ReplayRootInvocationID != root.ID ||
		child.ReplayRootCreatedAt == nil || !child.ReplayRootCreatedAt.Equal(root.CreatedAt) || child.Method != root.Method || child.Path != root.Path ||
		!bytes.Equal(child.Payload, root.Payload) || !jsonEquivalent(child.Headers, opts.Headers) || !jsonEquivalent(child.RetryPolicyJSON, opts.RetryPolicyJSON) ||
		child.Attempts != 0 || child.Result != nil || child.CompletedAt != nil || child.LeaseExpiresAt != nil || child.ReplayGeneration != 0 ||
		child.DeadlineAt == nil || !child.DeadlineAt.Equal(deadline.Truncate(time.Microsecond)) && !child.DeadlineAt.Equal(deadline) {
		t.Fatalf("captured request or fresh lifecycle lost: %+v", child)
	}
	child.Payload[0] = '!'
	*child.ReplayRootCreatedAt = time.Now().Add(-time.Hour)
	child, err = replayer.ExistingPlainInvocationReplay(ctx, accountID, root.ID)
	if err != nil || !bytes.Equal(child.Payload, root.Payload) || !child.ReplayRootCreatedAt.Equal(root.CreatedAt) {
		t.Fatalf("caller mutated durable replay: %+v %v", child, err)
	}
	failPlainReplay(t, ctx, store, child.ID)
	// Parent retention is independent of the surviving child's trusted root.
	if _, err := store.DeleteInvocationsByIDs(ctx, []string{root.ID}); err != nil {
		t.Fatal(err)
	}
	next, err := replayer.ReplayPlainInvocation(ctx, accountID, child.ID, opts)
	if err != nil || next.ID == child.ID || next.ReplayedFromInvocationID != child.ID || next.ReplayRootInvocationID != root.ID || !next.ReplayRootCreatedAt.Equal(root.CreatedAt) {
		t.Fatalf("subsequent recovery lost root after pruning: %+v %v", next, err)
	}
	if _, err := store.ClaimInvocationWithCap(ctx, next.ID, "", 30, 10); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteInvocation(ctx, next.ID, json.RawMessage(`{"recovered":true}`)); err != nil {
		t.Fatal(err)
	}
	duplicate, err := replayer.ReplayPlainInvocation(ctx, accountID, child.ID, state.PlainInvocationReplayOptions{Headers: json.RawMessage(`invalid`)})
	if err != nil || duplicate.ID != next.ID || duplicate.State != state.InvocationCompleted {
		t.Fatalf("completed child re-executed or admission rerun: %+v %v", duplicate, err)
	}
	if _, err := replayer.ReplayPlainInvocation(ctx, uuid.NewString(), child.ID, opts); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign replay: %v", err)
	}
	if _, err := store.DeleteInvocationsByIDs(ctx, []string{next.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := replayer.ReplayPlainInvocation(ctx, accountID, child.ID, opts); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("pruned child permitted another execution: %v", err)
	}
	// Even a same-owner, same-lineage reuse must not substitute for the child.
	if _, err := store.EnqueueInvocation(ctx, state.Invocation{ID: next.ID, AccountID: accountID, AppID: appID,
		DeploymentScope: child.DeploymentScope, Source: state.InvocationReplay, ReplayedFromInvocationID: child.ID, DueAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	for _, lookup := range []func() (state.Invocation, error){
		func() (state.Invocation, error) {
			return replayer.ExistingPlainInvocationReplay(ctx, accountID, child.ID)
		},
		func() (state.Invocation, error) {
			return replayer.ReplayPlainInvocation(ctx, accountID, child.ID, opts)
		},
	} {
		if _, err := lookup(); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("reused child ID accepted: %v", err)
		}
	}
}

func jsonEquivalent(a, b []byte) bool {
	var left, right any
	if json.Unmarshal(a, &left) != nil || json.Unmarshal(b, &right) != nil {
		return false
	}
	x, _ := json.Marshal(left)
	y, _ := json.Marshal(right)
	return bytes.Equal(x, y)
}

func TestPgPlainReplayRollbackAndOwnership(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	accountID, appID, _ := seedLiveDeploy(t, store, ctx)
	root, err := store.EnqueueInvocation(ctx, state.Invocation{AccountID: accountID, AppID: appID, Source: state.InvocationAsyncInvoke, DueAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	failPlainReplay(t, ctx, store, root.ID)
	// Simulate losing the transaction between insertion and durable recording.
	if _, err := pool.Exec(ctx, `CREATE FUNCTION reject_plain_replay() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected marker failure'; END $$;
		CREATE TRIGGER reject_plain_replay BEFORE INSERT ON invocation_plain_replays FOR EACH ROW EXECUTE FUNCTION reject_plain_replay()`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReplayPlainInvocation(ctx, accountID, root.ID, state.PlainInvocationReplayOptions{}); err == nil {
		t.Fatal("marker failure admitted a child")
	}
	var children, markers int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM invocations WHERE replayed_from_invocation_id=$1),
		(SELECT count(*) FROM invocation_plain_replays WHERE parent_invocation_id=$1)`, root.ID).Scan(&children, &markers); err != nil || children != 0 || markers != 0 {
		t.Fatalf("partial admission survived: children=%d markers=%d err=%v", children, markers, err)
	}
	if _, err := pool.Exec(ctx, "DROP TRIGGER reject_plain_replay ON invocation_plain_replays; DROP FUNCTION reject_plain_replay()"); err != nil {
		t.Fatal(err)
	}
	child, err := store.ReplayPlainInvocation(ctx, accountID, root.ID, state.PlainInvocationReplayOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "select count(*) from invocations where replayed_from_invocation_id=$1", root.ID).Scan(&children); err != nil || children != 1 {
		t.Fatalf("retry created %d children: %v", children, err)
	}
	foreign, err := store.CreateAccount(ctx, "plain-replay-foreign@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "update apps set account_id=$1 where id=$2", foreign.ID, appID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ExistingPlainInvocationReplay(ctx, accountID, root.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("old owner read durable child: %v", err)
	}
	if _, err := store.ReplayPlainInvocation(ctx, accountID, root.ID, state.PlainInvocationReplayOptions{}); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("old owner admitted child: %v", err)
	}
	retained, err := store.InvocationByID(ctx, child.ID)
	if err != nil || retained.ID != child.ID {
		t.Fatalf("ownership denial altered child: %+v %v", retained, err)
	}
}

func TestPgPlainReplayEligibility(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	accountID, appID, _ := seedLiveDeploy(t, store, ctx)
	for _, terminal := range []string{"pending", "dispatching", "completed", "cancelled", "superseded", "expired", "failed", "dead_letter"} {
		t.Run(terminal, func(t *testing.T) {
			root, err := store.EnqueueInvocation(ctx, state.Invocation{AccountID: accountID, AppID: appID, Source: state.InvocationAsyncInvoke, DueAt: time.Now()})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, "update invocations set state=$1 where id=$2", terminal, root.ID); err != nil {
				t.Fatal(err)
			}
			_, err = store.ReplayPlainInvocation(ctx, accountID, root.ID, state.PlainInvocationReplayOptions{})
			if terminal == "failed" || terminal == "dead_letter" {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, state.ErrPlainReplayNotAllowed) {
				t.Fatalf("nonfailed admission: %v", err)
			}
		})
	}
	keyed, err := store.EnqueueKeyedInvocation(ctx, state.Invocation{AccountID: accountID, AppID: appID, Source: state.InvocationAsyncInvoke, DueAt: time.Now()}, workpolicy.Policy{Name: "orders", MaxRunningPerKey: 1}, "s:order")
	if err != nil {
		t.Fatal(err)
	}
	failPlainReplay(t, ctx, store, keyed.ID)
	if _, err := store.ReplayPlainInvocation(ctx, accountID, keyed.ID, state.PlainInvocationReplayOptions{}); !errors.Is(err, state.ErrPlainReplayNotAllowed) {
		t.Fatalf("plain replay stripped policy: %v", err)
	}
}
