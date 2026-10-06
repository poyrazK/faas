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

// adr: 609
func TestMemKeyedReplayLifecycle(t *testing.T) {
	store := state.NewMemStore()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, "keyed-replay@tests.example", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: acct.ID, Slug: "keyed-replay"})
	if err != nil {
		t.Fatal(err)
	}
	assertKeyedReplayLifecycle(t, ctx, store, acct.ID, app.ID)
}

func TestPgKeyedReplayLifecycle(t *testing.T) {
	store, ctx, appID, accountID := seedInvocationPg(t)
	assertKeyedReplayLifecycle(t, ctx, store, accountID, appID)
}

func assertKeyedReplayLifecycle(t *testing.T, ctx context.Context, store state.Store, accountID, appID string) {
	t.Helper()
	replayer := store.(state.KeyedInvocationReplayStore)
	policy := workpolicy.Policy{Name: "orders", MaxRunningPerKey: 1, PendingUpdates: workpolicy.PendingKeepLatest,
		ExpiresAfter: time.Hour, MaxRunningPerFairnessKey: 1}
	enqueue := func(key string) state.Invocation {
		t.Helper()
		inv, err := store.EnqueueKeyedInvocation(ctx, state.Invocation{AppID: appID, AccountID: accountID,
			DeploymentScope: "staging", Source: state.InvocationAsyncInvoke, Method: "POST", Path: "/orders",
			Payload: json.RawMessage(`{"order":123}`), Headers: json.RawMessage(`{"x-order":"123"}`),
			WorkPolicyRevision: 42, RetryPolicyJSON: json.RawMessage(`{"max_attempts":3}`), DueAt: time.Now()}, policy, key, "s:customer-1")
		if err != nil {
			t.Fatal(err)
		}
		return inv
	}
	fail := func(id string) {
		t.Helper()
		if _, err := store.ClaimInvocationWithCap(ctx, id, "", 30, 10); err != nil {
			t.Fatal(err)
		}
		if err := store.FailInvocation(ctx, id, "handler failure", 0, 0, state.WithClaimAttempt(1)); err != nil {
			t.Fatal(err)
		}
	}
	root := enqueue("s:order-123")
	fail(root.ID)
	newer := enqueue("s:order-123")
	deadline, retention := time.Now().Add(time.Minute), time.Now().Add(time.Hour)
	opts := state.KeyedInvocationReplayOptions{DeadlineAt: &deadline, ResultRetentionUntil: &retention}
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan state.Invocation, 8)
	failures := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			inv, err := replayer.ReplayKeyedInvocation(ctx, accountID, root.ID, opts)
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
			t.Fatal("duplicate replay created multiple children")
		}
		child = inv
	}
	if child.ID == "" || child.ID == root.ID || child.WorkSequence <= newer.WorkSequence || child.Source != state.InvocationReplay ||
		child.WorkPolicyName != root.WorkPolicyName || child.WorkPolicyRevision != root.WorkPolicyRevision ||
		!bytes.Equal(child.WorkKeyDigest, root.WorkKeyDigest) || !bytes.Equal(child.WorkFairnessDigest, root.WorkFairnessDigest) ||
		child.WorkFairnessLimit != root.WorkFairnessLimit || !child.WorkExpiresAt.Equal(*root.WorkExpiresAt) ||
		child.DeploymentScope != "staging" || child.ReplayedFromInvocationID != root.ID || child.ReplayRootInvocationID != root.ID ||
		!child.ReplayRootCreatedAt.Equal(root.CreatedAt) || child.Method != root.Method || child.Path != root.Path ||
		!bytes.Equal(child.Payload, root.Payload) || !bytes.Equal(child.RetryPolicyJSON, root.RetryPolicyJSON) || child.Attempts != 0 {
		t.Fatalf("captured identity/lineage lost: %+v", child)
	}
	// Returned buffers/timestamps must not let a caller mutate persisted identity.
	child.WorkKeyDigest[0] ^= 255
	*child.WorkExpiresAt = time.Now().Add(-time.Hour)
	child, err := replayer.ReplayKeyedInvocation(ctx, accountID, root.ID, opts)
	if err != nil || !bytes.Equal(child.WorkKeyDigest, root.WorkKeyDigest) || !child.WorkExpiresAt.Equal(*root.WorkExpiresAt) {
		t.Fatalf("replay return value aliases storage: %+v %v", child, err)
	}
	if _, err := store.ClaimInvocationWithCap(ctx, child.ID, "", 30, 10); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("replay bypassed newer lane head: %v", err)
	}
	kept, err := store.InvocationByID(ctx, newer.ID)
	if err != nil || kept.State != state.InvocationPending {
		t.Fatalf("replay replaced newer work: %+v %v", kept, err)
	}
	if _, err := store.ClaimInvocationWithCap(ctx, newer.ID, "", 30, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimInvocationWithCap(ctx, child.ID, "", 30, 10); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("replay ran beside active same-key work: %v", err)
	}
	if err := store.CompleteKeyedInvocation(ctx, newer.ID, 1, nil); err != nil {
		t.Fatal(err)
	}
	other := enqueue("s:other-order")
	if _, err := store.ClaimInvocationWithCap(ctx, other.ID, "", 30, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimInvocationWithCap(ctx, child.ID, "", 30, 10); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("replay bypassed captured fairness cap: %v", err)
	}
	if err := store.CompleteKeyedInvocation(ctx, other.ID, 1, nil); err != nil {
		t.Fatal(err)
	}
	fail(child.ID)
	next, err := replayer.ReplayKeyedInvocation(ctx, accountID, child.ID, opts)
	if err != nil || next.ReplayedFromInvocationID != child.ID || next.ReplayRootInvocationID != root.ID || next.WorkSequence <= child.WorkSequence {
		t.Fatalf("subsequent replay lost lineage: %+v %v", next, err)
	}
	if _, err := store.ClaimInvocationWithCap(ctx, next.ID, "", 30, 10); err != nil {
		t.Fatal(err)
	}
	// A late completion for an older execution cannot finish the new child.
	if err := store.CompleteKeyedInvocation(ctx, root.ID, 1, json.RawMessage(`{"stale":true}`)); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("stale completion accepted: %v", err)
	}
	active, err := store.InvocationByID(ctx, next.ID)
	if err != nil || active.State != state.InvocationDispatching {
		t.Fatalf("stale completion changed replay: %+v %v", active, err)
	}
	if err := store.CompleteKeyedInvocation(ctx, next.ID, 1, nil); err != nil {
		t.Fatal(err)
	}
	duplicate, err := replayer.ReplayKeyedInvocation(ctx, accountID, child.ID, opts)
	if err != nil || duplicate.ID != next.ID || duplicate.State != state.InvocationCompleted {
		t.Fatalf("completed child re-executed: %+v %v", duplicate, err)
	}
	if _, err := replayer.ReplayKeyedInvocation(ctx, uuid.NewString(), root.ID, opts); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign replay = %v", err)
	}
	if _, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: appID, AccountID: accountID, DeploymentScope: root.DeploymentScope,
		Source: state.InvocationReplay, ReplayedFromInvocationID: child.ID, DueAt: time.Now()}); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("unkeyed store replay accepted keyed parent: %v", err)
	}
	if _, err := store.DeleteInvocationsByIDs(ctx, []string{next.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := replayer.ReplayKeyedInvocation(ctx, accountID, child.ID, opts); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("pruned child allowed another execution: %v", err)
	}
	// A reused ID cannot turn the historical marker into a foreign execution.
	if _, err := store.EnqueueInvocation(ctx, state.Invocation{ID: next.ID, AccountID: accountID, AppID: appID,
		Source: state.InvocationAsyncInvoke, DueAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := replayer.ExistingKeyedInvocationReplay(ctx, accountID, child.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("reused child ID was returned as recovery: %v", err)
	}
	if _, err := replayer.ReplayKeyedInvocation(ctx, accountID, child.ID, opts); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("reused child ID was accepted as recovery: %v", err)
	}
}

func TestPgKeyedReplayExpiryAndEligibility(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	accountID, appID, _ := seedLiveDeploy(t, store, ctx)
	policy := workpolicy.Policy{Name: "expiry", MaxRunningPerKey: 1}
	for _, deadline := range []string{"work_expires_at", "start_deadline_at"} {
		t.Run(deadline, func(t *testing.T) {
			inv, err := store.EnqueueKeyedInvocation(ctx, state.Invocation{AppID: appID, AccountID: accountID,
				Source: state.InvocationAsyncInvoke, DueAt: time.Now()}, policy, "s:"+deadline)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.ClaimInvocationWithCap(ctx, inv.ID, "", 30, 10); err != nil {
				t.Fatal(err)
			}
			if err := store.FailInvocation(ctx, inv.ID, "failure", 0, 0, state.WithClaimAttempt(1)); err != nil {
				t.Fatal(err)
			}
			query := "update invocations set " + deadline + "=clock_timestamp()-interval '1 second' where id=$1"
			if _, err := pool.Exec(ctx, query, inv.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := store.ReplayKeyedInvocation(ctx, accountID, inv.ID, state.KeyedInvocationReplayOptions{}); !errors.Is(err, state.ErrKeyedReplayExpired) {
				t.Fatalf("expired replay = %v", err)
			}
			var count int
			if err := pool.QueryRow(ctx, "select count(*) from invocation_keyed_replays where parent_invocation_id=$1", inv.ID).Scan(&count); err != nil || count != 0 {
				t.Fatalf("expiry mutated ledger: %d %v", count, err)
			}
		})
	}
	for _, terminal := range []string{"pending", "dispatching", "completed", "cancelled", "superseded", "expired", "dead_letter"} {
		t.Run(terminal, func(t *testing.T) {
			inv, err := store.EnqueueKeyedInvocation(ctx, state.Invocation{AppID: appID, AccountID: accountID,
				Source: state.InvocationAsyncInvoke, DueAt: time.Now()}, policy, "s:"+terminal)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, "update invocations set state=$2 where id=$1", inv.ID, terminal); err != nil {
				t.Fatal(err)
			}
			if _, err := store.ReplayKeyedInvocation(ctx, accountID, inv.ID, state.KeyedInvocationReplayOptions{}); !errors.Is(err, state.ErrKeyedReplayNotAllowed) {
				t.Fatalf("%s replay = %v", terminal, err)
			}
		})
	}
}

func TestPgKeyedReplayOwnershipAndPrunedParent(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	accountID, appID, _ := seedLiveDeploy(t, store, ctx)
	root, err := store.EnqueueKeyedInvocation(ctx, state.Invocation{AppID: appID, AccountID: accountID,
		Source: state.InvocationAsyncInvoke, DueAt: time.Now()}, workpolicy.Policy{Name: "ownership", MaxRunningPerKey: 1}, "s:order-123")
	if err != nil {
		t.Fatal(err)
	}
	fail := func(id string) {
		t.Helper()
		claim, err := store.ClaimInvocationWithCap(ctx, id, "", 30, 10)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.FailInvocation(ctx, id, "failure", 0, 0, state.WithClaimAttempt(claim.Attempts)); err != nil {
			t.Fatal(err)
		}
	}
	fail(root.ID)
	child, err := store.ReplayKeyedInvocation(ctx, accountID, root.ID, state.KeyedInvocationReplayOptions{})
	if err != nil {
		t.Fatal(err)
	}
	fail(child.ID)
	if _, err := store.DeleteInvocationsByIDs(ctx, []string{root.ID}); err != nil {
		t.Fatal(err)
	}
	var markers int
	if err := pool.QueryRow(ctx, "select count(*) from invocation_keyed_replays where parent_invocation_id=$1", root.ID).Scan(&markers); err != nil || markers != 0 {
		t.Fatalf("parent marker did not cascade: %d %v", markers, err)
	}
	grandchild, err := store.ReplayKeyedInvocation(ctx, accountID, child.ID, state.KeyedInvocationReplayOptions{})
	if err != nil || grandchild.ReplayRootInvocationID != root.ID || !grandchild.ReplayRootCreatedAt.Equal(root.CreatedAt) {
		t.Fatalf("pruned root severed lineage: %+v %v", grandchild, err)
	}
	other, err := store.CreateAccount(ctx, "new-owner@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "update apps set account_id=$1 where id=$2", other.ID, appID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ExistingKeyedInvocationReplay(ctx, accountID, child.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("transferred app disclosed child: %v", err)
	}
	if _, err := store.ReplayKeyedInvocation(ctx, accountID, child.ID, state.KeyedInvocationReplayOptions{}); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("transferred app replay = %v", err)
	}
}

func TestPgKeyedReplayOrdersBehindConcurrentBrokerClaim(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	accountID, appID, _ := seedLiveDeploy(t, store, ctx)
	policy, err := store.UpsertAppWorkPolicy(ctx, accountID, appID, workpolicy.Policy{Name: "broker-orders", MaxRunningPerKey: 1})
	if err != nil {
		t.Fatal(err)
	}
	root, err := store.EnqueueKeyedInvocation(ctx, state.Invocation{AppID: appID, AccountID: accountID,
		Source: state.InvocationAsyncInvoke, WorkPolicyRevision: policy.Revision, DueAt: time.Now()}, policy.Policy, "s:order-123")
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.ClaimInvocationWithCap(ctx, root.ID, "", 30, 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FailInvocation(ctx, root.ID, "failure", 0, 0, state.WithClaimAttempt(claim.Attempts)); err != nil {
		t.Fatal(err)
	}
	trigger, err := store.CreateTriggerIfUnderQuota(ctx, appID, "kafka", "broker-orders", true, []byte(`{}`), "", 10, 1000, 3, 1<<20, "commit", api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatal(err)
	}
	item := "partition-0-offset-1"
	brokerID, err := store.InsertKeyedTriggerRecord(ctx, trigger.ID.String(), item, []byte(`{}`), nil, nil, policy, "s:order-123")
	if err != nil {
		t.Fatal(err)
	}
	var brokerSequence int64
	if err := pool.QueryRow(ctx, "select work_sequence from trigger_records where id=$1", brokerID).Scan(&brokerSequence); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	replays := make(chan state.Invocation, 1)
	errorsCh := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		child, err := store.ReplayKeyedInvocation(ctx, accountID, root.ID, state.KeyedInvocationReplayOptions{})
		if err != nil {
			errorsCh <- err
			return
		}
		replays <- child
	}()
	go func() {
		defer wg.Done()
		<-start
		records, err := store.ClaimTriggerRecordsByItems(ctx, trigger.ID.String(), []string{item})
		if err != nil {
			errorsCh <- err
			return
		}
		if len(records) != 1 {
			errorsCh <- errors.New("broker head was not claimed")
		}
	}()
	close(start)
	wg.Wait()
	close(errorsCh)
	for err := range errorsCh {
		t.Fatal(err)
	}
	child := <-replays
	if child.WorkSequence <= brokerSequence {
		t.Fatalf("replay sequence %d precedes broker %d", child.WorkSequence, brokerSequence)
	}
	if _, err := store.ClaimInvocationWithCap(ctx, child.ID, "", 30, 10); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("replay bypassed claimed broker head: %v", err)
	}
	var generation int64
	if err := pool.QueryRow(ctx, "select claim_generation from trigger_records where id=$1", brokerID).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteClaimedTriggerRecord(ctx, brokerID, generation); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimInvocationWithCap(ctx, child.ID, "", 30, 10); err != nil {
		t.Fatalf("replay remained blocked after broker completion: %v", err)
	}
}
