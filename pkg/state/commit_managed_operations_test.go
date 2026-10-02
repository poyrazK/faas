// adr: 430
package state_test

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/exclusivework"
	"github.com/onebox-faas/faas/pkg/state"
)

func commitManagedFixture(t *testing.T) (*state.PgStore, *pgxpool.Pool, state.CommitSource, state.Invocation) {
	t.Helper()
	ctx := t.Context()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	acct, err := store.CreateAccount(ctx, "managed-commit-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "commit-" + uuid.NewString()[:8], Type: state.AppTypeApp, Runtime: "node22", RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.UpsertExclusiveWorkPolicy(ctx, acct.ID, exclusivework.Policy{
		Name: "orders", Scope: "account", Contention: "queue", MemberAppIDs: []string{app.ID},
		LeaseSeconds: 15, MaxAttemptSeconds: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	source, err := store.CreateCommitSource(ctx, state.CommitSource{AccountID: acct.ID, AppID: app.ID, Name: "orders", OperationPolicy: "orders"})
	if err != nil {
		t.Fatal(err)
	}
	inv := state.Invocation{AccountID: acct.ID, AppID: app.ID, Method: "POST", Path: "/", Payload: json.RawMessage(`{"order_id":1}`)}
	return store, pool, source, inv
}

func TestPgCommitMigrationReplayPreservesManagedIdentity(t *testing.T) {
	store, pool, source, inv := commitManagedFixture(t)
	ctx := t.Context()
	event := uuid.NewString()
	original, err := store.AcceptCommitOperation(ctx, source.AccountID, source.ID, event, "order.created", inv.Payload, inv)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM goose_db_version WHERE version_id IN (20261001224800001,20261002004531001)`); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("replay migrations with an accepted managed event: %v", err)
	}
	repeated, err := store.AcceptCommitOperation(ctx, source.AccountID, source.ID, event, "order.created", inv.Payload, inv)
	if err != nil || repeated.ID != original.ID || repeated.OperationID != original.OperationID || repeated.InvocationID != "" {
		t.Fatalf("migration replay reopened work: original=%+v repeated=%+v err=%v", original, repeated, err)
	}
	var owners int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM exclusive_work_operations WHERE account_id=$1::uuid`, source.AccountID).Scan(&owners); err != nil || owners != 1 {
		t.Fatalf("operation owners=%d err=%v", owners, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE exclusive_work_operations SET state='completed', completed_at=now() WHERE id=$1::uuid`, original.OperationID); err != nil {
		t.Fatal(err)
	}
	history, err := store.CommitOperationByID(ctx, source.AccountID, original.OperationID)
	if err != nil || history.State != "completed" || history.CompletedAt == nil {
		t.Fatalf("migration replay lost completion tracking: history=%+v err=%v", history, err)
	}
}

func TestPgCommitManagedOperationAtomicReplayAndCompletion(t *testing.T) {
	store, pool, source, inv := commitManagedFixture(t)
	ctx := t.Context()
	event := uuid.NewString()
	receipts := make(chan state.CommitReceipt, 8)
	failures := make(chan error, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			receipt, err := store.AcceptCommitEvent(ctx, source.AccountID, source.ID, event, "order.created", inv.Payload, inv, 1)
			receipts <- receipt
			failures <- err
		}()
	}
	wg.Wait()
	close(receipts)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	var original state.CommitReceipt
	for receipt := range receipts {
		if original.ID == "" {
			original = receipt
		}
		if receipt.ID != original.ID || receipt.OperationID != original.OperationID || receipt.InvocationID != "" || receipt.OperationID == "" {
			t.Fatalf("receipt did not identify one managed operation: %+v, original=%+v", receipt, original)
		}
	}
	var operations, invocations, receiptRows int
	if err := pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM exclusive_work_operations),(SELECT count(*) FROM invocations),(SELECT count(*) FROM commit_receipts)").Scan(&operations, &invocations, &receiptRows); err != nil {
		t.Fatal(err)
	}
	if operations != 1 || invocations != 0 || receiptRows != 1 {
		t.Fatalf("operations=%d invocations=%d receipts=%d", operations, invocations, receiptRows)
	}
	operation, err := store.ExclusiveOperationByID(ctx, source.AccountID, original.OperationID)
	if err != nil || operation.State != "pending" || operation.Policy.Name != "orders" {
		t.Fatalf("managed operation=%+v err=%v", operation, err)
	}
	var request api.InvokeRequest
	var acceptedPayload struct {
		OrderID int `json:"order_id"`
	}
	if err := json.Unmarshal(operation.Request, &request); err != nil || request.Method != "POST" || json.Unmarshal(request.Payload, &acceptedPayload) != nil || acceptedPayload.OrderID != 1 {
		t.Fatalf("accepted dispatch=%+v err=%v", request, err)
	}
	if _, err := store.AcceptCommitOperation(ctx, source.AccountID, source.ID, event, "order.changed", inv.Payload, inv); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("changed event content: %v", err)
	}
	if _, err := store.CommitReceiptByEvent(ctx, uuid.NewString(), source.ID, event); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign receipt read: %v", err)
	}
	if _, err := store.SetCommitSourceEnabled(ctx, source.AccountID, source.ID, false); err != nil {
		t.Fatal(err)
	}
	replay, err := store.AcceptCommitOperation(ctx, source.AccountID, source.ID, event, "order.created", json.RawMessage(`{ "order_id": 1 }`), state.Invocation{})
	if err != nil || replay.OperationID != original.OperationID || replay.ID != original.ID {
		t.Fatalf("paused canonical replay=%+v err=%v", replay, err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: source.AppID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:commit-managed-test", Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	instance, err := store.CreateInstance(ctx, source.AppID, dep.ID, string(state.StateRunning), 256, node.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.ClaimExclusiveOperation(ctx, source.AccountID, original.OperationID, state.ExclusiveIncarnation(instance))
	if err != nil {
		t.Fatal(err)
	}
	history, err := store.CommitOperationByID(ctx, source.AccountID, original.OperationID)
	if err != nil || history.State != "running" {
		t.Fatalf("claimed receipt history=%+v err=%v", history, err)
	}
	if err := store.CommitExclusiveOperation(ctx, claim, json.RawMessage(`{"processed":true}`), nil); err != nil {
		t.Fatal(err)
	}
	history, err = store.CommitOperationByID(ctx, source.AccountID, original.OperationID)
	if err != nil || history.State != "completed" || history.CompletedAt == nil {
		t.Fatalf("completed receipt history=%+v err=%v", history, err)
	}
	if _, err := store.RetireExclusiveWorkPolicy(ctx, source.AccountID, "orders"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetCommitSourceEnabled(ctx, source.AccountID, source.ID, true); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("retired policy source resumed: %v", err)
	}
	replay, err = store.AcceptCommitOperation(ctx, source.AccountID, source.ID, event, "order.created", inv.Payload, state.Invocation{})
	if err != nil || replay.OperationID != original.OperationID {
		t.Fatalf("retired-policy replay=%+v err=%v", replay, err)
	}
	// Owner cleanup removes dependent idempotency submissions before operations.
	// The independent Commit receipt must still preserve the original identity.
	if _, err := pool.Exec(ctx, `DELETE FROM exclusive_work_submissions WHERE operation_id=$1::uuid`, original.OperationID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM exclusive_work_operations WHERE id=$1::uuid`, original.OperationID); err != nil {
		t.Fatal(err)
	}
	history, err = store.CommitOperationByID(ctx, source.AccountID, original.OperationID)
	if err != nil || history.State != "completed" || history.CompletedAt == nil {
		t.Fatalf("retained receipt after owner cleanup=%+v err=%v", history, err)
	}
	replay, err = store.AcceptCommitOperation(ctx, source.AccountID, source.ID, event, "order.created", inv.Payload, state.Invocation{})
	if err != nil || replay.ID != original.ID || replay.OperationID != original.OperationID {
		t.Fatalf("owner cleanup reopened accepted work=%+v err=%v", replay, err)
	}
}

func TestPgCommitManagedOperationReceiptFailureRollsBackAdmission(t *testing.T) {
	store, pool, source, inv := commitManagedFixture(t)
	ctx := t.Context()
	// Force the receipt write to fail after the owner inserted its operation and
	// identity. An outer transaction must roll back all of those writes together.
	if _, err := pool.Exec(ctx, "ALTER TABLE commit_receipts ADD CONSTRAINT receipt_fault CHECK (event_type <> 'test.receipt-fault')"); err != nil {
		t.Fatal(err)
	}
	event := uuid.NewString()
	if _, err := store.AcceptCommitOperation(ctx, source.AccountID, source.ID, event, "test.receipt-fault", inv.Payload, inv); err == nil {
		t.Fatal("receipt fault did not fail acceptance")
	}
	var operations, receipts, keys, submissions int
	if err := pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM exclusive_work_operations),(SELECT count(*) FROM commit_receipts),(SELECT count(*) FROM exclusive_work_keys),(SELECT count(*) FROM exclusive_work_submissions)").Scan(&operations, &receipts, &keys, &submissions); err != nil {
		t.Fatal(err)
	}
	if operations != 0 || receipts != 0 || keys != 0 || submissions != 0 {
		t.Fatalf("failed admission leaked operations=%d receipts=%d keys=%d submissions=%d", operations, receipts, keys, submissions)
	}
	if _, err := pool.Exec(ctx, "ALTER TABLE commit_receipts DROP CONSTRAINT receipt_fault"); err != nil {
		t.Fatal(err)
	}
	receipt, err := store.AcceptCommitOperation(ctx, source.AccountID, source.ID, event, "test.receipt-fault", inv.Payload, inv)
	if err != nil || receipt.OperationID == "" {
		t.Fatalf("receipt recovery=%+v err=%v", receipt, err)
	}
}

func TestPgCommitManagedSourcePolicyScopeAndImmutableDestination(t *testing.T) {
	store, _, source, _ := commitManagedFixture(t)
	ctx := t.Context()
	repeated, err := store.CreateCommitSource(ctx, source)
	if err != nil || repeated.ID != source.ID {
		t.Fatalf("source replay=%+v err=%v", repeated, err)
	}
	if _, err := store.CreateCommitSource(ctx, state.CommitSource{AccountID: source.AccountID, AppID: source.AppID, Name: source.Name}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("managed source downgraded to legacy: %v", err)
	}
	for _, policy := range []exclusivework.Policy{
		{Name: "reject", Scope: "account", Contention: "reject", MemberAppIDs: []string{source.AppID}, LeaseSeconds: 15, MaxAttemptSeconds: 60},
		{Name: "tenant", Scope: "platform_tenant", Contention: "queue", MemberAppIDs: []string{source.AppID}, LeaseSeconds: 15, MaxAttemptSeconds: 60},
	} {
		if _, err := store.UpsertExclusiveWorkPolicy(ctx, source.AccountID, policy); err != nil {
			t.Fatal(err)
		}
		if _, err := store.CreateCommitSource(ctx, state.CommitSource{AccountID: source.AccountID, AppID: source.AppID, Name: policy.Name, OperationPolicy: policy.Name}); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("unsupported source policy %q: %v", policy.Name, err)
		}
	}
	if _, err := store.UpsertExclusiveWorkPolicy(ctx, source.AccountID, exclusivework.Policy{Name: "another", Scope: "account", Contention: "queue", MemberAppIDs: []string{source.AppID}, LeaseSeconds: 15, MaxAttemptSeconds: 60}); err != nil {
		t.Fatal(err)
	}
	changed := source
	changed.OperationPolicy = "another"
	if _, err := store.CreateCommitSource(ctx, changed); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("source policy reassignment: %v", err)
	}
	foreign, err := store.CreateAccount(ctx, uuid.NewString()+"@commit-policy.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateCommitSource(ctx, state.CommitSource{AccountID: foreign.ID, AppID: source.AppID, Name: "foreign", OperationPolicy: "orders"}); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign source binding: %v", err)
	}
}

func TestPgCommitManagedSourcePolicyLifecycle(t *testing.T) {
	store, _, source, _ := commitManagedFixture(t)
	ctx := t.Context()
	if _, err := store.RetireExclusiveWorkPolicy(ctx, source.AccountID, "orders"); !errors.Is(err, state.ErrExclusivePolicyInUse) {
		t.Fatalf("enabled source allowed policy retirement: %v", err)
	}
	if _, err := store.UpsertExclusiveWorkPolicy(ctx, source.AccountID, exclusivework.Policy{
		Name: "orders", Scope: "account", Contention: "reject", MemberAppIDs: []string{source.AppID}, LeaseSeconds: 15, MaxAttemptSeconds: 60,
	}); !errors.Is(err, state.ErrExclusivePolicyInUse) {
		t.Fatalf("enabled source allowed incompatible policy change: %v", err)
	}
	if _, err := store.SetCommitSourceEnabled(ctx, source.AccountID, source.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RetireExclusiveWorkPolicy(ctx, source.AccountID, "orders"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetCommitSourceEnabled(ctx, source.AccountID, source.ID, true); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("retired policy source resumed: %v", err)
	}
}
