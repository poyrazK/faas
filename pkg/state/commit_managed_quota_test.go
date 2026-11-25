package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Exercise the actual account-wide limit with two independent PostgreSQL
// connections, rather than substituting a smaller in-memory quota.
func TestPgCommitManagedOperationMixedProducerQuota(t *testing.T) {
	store, pool, source, invocation := commitManagedFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()

	existingEvent := uuid.NewString()
	existing, err := store.AcceptCommitEvent(ctx, source.AccountID, source.ID, existingEvent, "order.created", invocation.Payload, invocation, 1)
	if err != nil || existing.ID == "" || existing.OperationID == "" {
		t.Fatalf("initial Commit receipt: %+v, %v", existing, err)
	}
	seed, joined, err := store.AdmitExclusiveOperation(ctx, state.ExclusiveAdmission{
		AccountID: source.AccountID, AppID: source.AppID, PolicyName: "orders",
		Key: json.RawMessage(`"quota-seed"`), Request: json.RawMessage(`{"kind":"seed"}`), IdempotencyKey: "quota-seed",
	})
	if err != nil || joined || seed.ID == "" {
		t.Fatalf("seed Operation: %+v joined=%t err=%v", seed, joined, err)
	}
	// Fill all but one slot by cloning a valid pending Operation into the seed
	// lane. The contenders use different keys, so the synthetic sequence does
	// not alter either admission path's own key or idempotency state.
	seedCount := api.MaxExclusivePendingPerAccount - 3
	tag, err := pool.Exec(ctx, `INSERT INTO exclusive_work_operations
 (id,account_id,key_id,app_id,sequence,state,policy_revision,configuration,request,request_digest)
 SELECT gen_random_uuid(),o.account_id,o.key_id,o.app_id,o.sequence+n,'pending',o.policy_revision,o.configuration,o.request,o.request_digest
 FROM exclusive_work_operations o CROSS JOIN generate_series(1,$2::integer) AS n
 WHERE o.id=$1::uuid`, seed.ID, seedCount)
	if err != nil || tag.RowsAffected() != int64(seedCount) {
		t.Fatalf("seed pending quota: rows=%d err=%v", tag.RowsAffected(), err)
	}

	makeContender := func(name string) *state.PgStore {
		t.Helper()
		cfg := pool.Config().Copy()
		cfg.MaxConns = 1
		cfg.MinConns = 0
		if cfg.ConnConfig.RuntimeParams == nil {
			cfg.ConnConfig.RuntimeParams = make(map[string]string)
		}
		cfg.ConnConfig.RuntimeParams["application_name"] = name
		contender, err := pgxpool.NewWithConfig(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(contender.Close)
		return state.NewPgStore(contender)
	}
	suffix := uuid.NewString()[:8]
	commitName, normalName := "commit-quota-"+suffix, "operation-quota-"+suffix
	commitStore, normalStore := makeContender(commitName), makeContender(normalName)
	guard, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = guard.Rollback(context.Background()) }()
	var accountID string
	if err := guard.QueryRow(ctx, `SELECT id::text FROM accounts WHERE id=$1::uuid FOR UPDATE`, source.AccountID).Scan(&accountID); err != nil {
		t.Fatal(err)
	}

	type outcome struct {
		producer  string
		receipt   state.CommitReceipt
		operation state.ExclusiveOperation
		joined    bool
		err       error
	}
	contestedEvent := uuid.NewString()
	results := make(chan outcome, 2)
	go func() {
		r, err := commitStore.AcceptCommitEvent(ctx, source.AccountID, source.ID, contestedEvent, "order.created", invocation.Payload, invocation, 1)
		results <- outcome{producer: "commit", receipt: r, err: err}
	}()
	go func() {
		op, joined, err := normalStore.AdmitExclusiveOperation(ctx, state.ExclusiveAdmission{
			AccountID: source.AccountID, AppID: source.AppID, PolicyName: "orders",
			Key: json.RawMessage(`"quota-contender"`), Request: json.RawMessage(`{"kind":"other"}`), IdempotencyKey: "quota-contender",
		})
		results <- outcome{producer: "operation", operation: op, joined: joined, err: err}
	}()
	// Both SQL sessions must actually be waiting on the account lock before it
	// is released. This forces overlapping admission attempts without relying
	// on scheduler timing or probabilistic retries.
	waitDeadline := time.Now().Add(20 * time.Second)
	for {
		var waiting int
		err := pool.QueryRow(ctx, `SELECT count(DISTINCT application_name) FROM pg_stat_activity
 WHERE application_name=ANY($1::text[]) AND wait_event_type='Lock'
 AND query ILIKE '%from accounts%' AND query ILIKE '%for update%'`, []string{commitName, normalName}).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting == 2 {
			break
		}
		if time.Now().After(waitDeadline) {
			t.Fatalf("contenders did not both wait on the account lock: waiting=%d", waiting)
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(25 * time.Millisecond):
		}
	}
	if err := guard.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var accepted, rejected int
	var commitWon, normalWon bool
	for range 2 {
		var result outcome
		select {
		case result = <-results:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		switch {
		case result.err == nil:
			accepted++
			if result.producer == "commit" {
				commitWon = true
				if result.receipt.ID == "" || result.receipt.OperationID == "" {
					t.Fatalf("accepted Commit lacks receipt or Operation: %+v", result.receipt)
				}
			} else {
				normalWon = true
				if result.joined || result.operation.ID == "" {
					t.Fatalf("accepted Operation: %+v joined=%t", result.operation, result.joined)
				}
			}
		case errors.Is(result.err, state.ErrQuotaExceeded):
			rejected++
		default:
			t.Fatalf("%s admission failed unexpectedly: %v", result.producer, result.err)
		}
	}
	if accepted != 1 || rejected != 1 {
		t.Fatalf("last-slot race: accepted=%d rejected=%d", accepted, rejected)
	}
	if !commitWon {
		if _, err := store.CommitReceiptByEvent(ctx, source.AccountID, source.ID, contestedEvent); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("rejected competing Commit left a receipt: %v", err)
		}
	}

	type counts struct{ operations, pending, receipts, keys, submissions int }
	readCounts := func() counts {
		t.Helper()
		var c counts
		err := pool.QueryRow(ctx, `SELECT
 (SELECT count(*) FROM exclusive_work_operations WHERE account_id=$1::uuid),
 (SELECT count(*) FROM exclusive_work_operations WHERE account_id=$1::uuid AND state IN ('pending','running')),
 (SELECT count(*) FROM commit_receipts WHERE account_id=$1::uuid),
 (SELECT count(*) FROM exclusive_work_keys WHERE account_id=$1::uuid),
 (SELECT count(*) FROM exclusive_work_submissions s JOIN exclusive_work_keys k ON k.id=s.key_id WHERE k.account_id=$1::uuid)`, source.AccountID).
			Scan(&c.operations, &c.pending, &c.receipts, &c.keys, &c.submissions)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	full := readCounts()
	wantReceipts, wantKeys := 1, 2
	if commitWon {
		wantReceipts++
	}
	if normalWon {
		wantKeys++
	}
	if full != (counts{api.MaxExclusivePendingPerAccount, api.MaxExclusivePendingPerAccount, wantReceipts, wantKeys, 3}) {
		t.Fatalf("quota race left unexpected durable state: %+v", full)
	}

	rejectedEvent := uuid.NewString()
	if _, err := store.AcceptCommitEvent(ctx, source.AccountID, source.ID, rejectedEvent, "order.created", invocation.Payload, invocation, 1); !errors.Is(err, state.ErrQuotaExceeded) {
		t.Fatalf("new Commit at quota: %v", err)
	}
	if _, err := store.CommitReceiptByEvent(ctx, source.AccountID, source.ID, rejectedEvent); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("quota rejection left a Commit receipt: %v", err)
	}
	if got := readCounts(); got != full {
		t.Fatalf("quota rejection changed durable state: before=%+v after=%+v", full, got)
	}
	replayed, err := store.AcceptCommitEvent(ctx, source.AccountID, source.ID, existingEvent, "order.created", invocation.Payload, state.Invocation{}, 0)
	if err != nil || replayed.ID != existing.ID || replayed.OperationID != existing.OperationID {
		t.Fatalf("replay at quota changed identity: %+v err=%v", replayed, err)
	}
	if got := readCounts(); got != full {
		t.Fatalf("replay at quota created durable work: before=%+v after=%+v", full, got)
	}

	if err := store.CancelExclusiveOperation(ctx, source.AccountID, seed.ID); err != nil {
		t.Fatal(fmt.Errorf("free pending quota: %w", err))
	}
	if got := readCounts(); got.pending != api.MaxExclusivePendingPerAccount-1 {
		t.Fatalf("cancellation did not free exactly one slot: %+v", got)
	}
	retried, err := store.AcceptCommitEvent(ctx, source.AccountID, source.ID, rejectedEvent, "order.created", invocation.Payload, invocation, 1)
	if err != nil || retried.ID == "" || retried.OperationID == "" || retried.ID == existing.ID || retried.OperationID == existing.OperationID {
		t.Fatalf("retry after capacity returns: %+v err=%v", retried, err)
	}
	final := readCounts()
	if final != (counts{api.MaxExclusivePendingPerAccount + 1, api.MaxExclusivePendingPerAccount, full.receipts + 1, full.keys, full.submissions + 1}) {
		t.Fatalf("retry left unexpected durable state: %+v", final)
	}
	replayed, err = store.AcceptCommitEvent(ctx, source.AccountID, source.ID, rejectedEvent, "order.created", invocation.Payload, state.Invocation{}, 0)
	if err != nil || replayed.ID != retried.ID || replayed.OperationID != retried.OperationID || readCounts() != final {
		t.Fatalf("retry replay at quota changed state: %+v err=%v", replayed, err)
	}
}
