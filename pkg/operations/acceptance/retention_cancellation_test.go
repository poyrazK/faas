package acceptance_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

type operationRetentionFixture interface {
	operationLifecycleStore
	state.OperationRetentionStore
	state.OperationExecutionStampStore
	UpdateAccountPlan(context.Context, string, api.Plan) error
}

func testOperationRetentionCancellation(t *testing.T, s operationRetentionFixture) {
	ctx, acct, app, def, alice, bob := operationFixture(t, s)
	admission := state.OperationAdmission{AccountID: acct.ID, PlatformTenantID: alice.ID, DefinitionID: def.ID, IdempotencyKey: "bounded-identity", Input: json.RawMessage(`{"count":1}`)}
	op, _, err := s.AdmitOperation(ctx, admission)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CancelOperation(ctx, acct.ID, bob.ID, op.ID, 1); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-customer cancellation: %v", err)
	}
	if _, err := s.CancelOperation(ctx, acct.ID, alice.ID, op.ID, 2); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale generation cancellation: %v", err)
	}
	cancelled, err := s.CancelOperation(ctx, acct.ID, alice.ID, op.ID, 1)
	if err != nil || cancelled.State != api.OperationCancelled {
		t.Fatalf("pending cancellation: %+v %v", cancelled, err)
	}
	duplicate, err := s.CancelOperation(ctx, acct.ID, alice.ID, op.ID, 1)
	if err != nil || duplicate.LatestSequence != cancelled.LatestSequence {
		t.Fatalf("cancellation replay: %+v %v", duplicate, err)
	}
	if n, err := s.PruneOperationState(ctx, cancelled.ExpiresAt.Add(time.Second), 500); err != nil || n != 1 {
		t.Fatalf("result retention: %d %v", n, err)
	}
	originalExecution, err := s.InvocationByID(ctx, op.CurrentInvocationID)
	if err != nil || !state.InvocationHasOperation(originalExecution) || originalExecution.OperationID != op.ID {
		t.Fatalf("execution lost recovery boundary after projection expiry: %+v %v", originalExecution, err)
	}
	if _, _, err := s.AdmitOperation(ctx, admission); !errors.Is(err, state.ErrOperationExpired) {
		t.Fatalf("result deletion regenerated business work: %v", err)
	}
	cutoff := op.CreatedAt.Add(time.Duration(op.PlanLimits.IdempotencyRetentionSeconds+1) * time.Second)
	if _, err := s.PruneOperationState(ctx, cutoff, 500); err != nil {
		t.Fatal(err)
	}
	fresh, created, err := s.AdmitOperation(ctx, admission)
	if err != nil || !created || fresh.ID == op.ID {
		t.Fatalf("identity not released after its documented window: %+v %v", fresh, err)
	}
	nodeID := uuid.NewString()
	if node, err := s.ComputeNodeByName(ctx, state.DefaultLocalNodeName); err == nil {
		nodeID = node.ID
	}
	instance, err := s.CreateInstance(ctx, app.ID, def.DeploymentID, "stopped", 128, nodeID, "")
	if err != nil {
		t.Fatal(err)
	}
	inv, err := s.ClaimInvocationWithCap(ctx, fresh.CurrentInvocationID, instance.ID, 60, 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.StampOperationExecutionAttempt(ctx, inv.ID, instance.ID, inv.Attempts+1); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("stale wake stamped replacement authority: %v", err)
	}
	if err := s.StampOperationExecutionAttempt(ctx, inv.ID, instance.ID, inv.Attempts); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateAccountPlan(ctx, acct.ID, api.PlanFree); err != nil {
		t.Fatal(err)
	}
	replayed, created, err := s.AdmitOperation(ctx, admission)
	if err != nil || created || replayed.ID != fresh.ID {
		t.Fatalf("plan downgrade broke retained idempotency: %+v %v %v", replayed, created, err)
	}
	newAdmission := admission
	newAdmission.IdempotencyKey = "new-after-downgrade"
	if _, _, err := s.AdmitOperation(ctx, newAdmission); !errors.Is(err, state.ErrOperationQuota) {
		t.Fatalf("downgraded account admitted fresh operation: %v", err)
	}
	if err := s.CompleteKeyedInvocation(ctx, inv.ID, inv.Attempts, json.RawMessage(`{"file":"retained.csv"}`)); err != nil {
		t.Fatalf("admitted contract invalidated by plan downgrade: %v", err)
	}
	completed, err := s.OperationByID(ctx, acct.ID, alice.ID, fresh.ID)
	if err != nil || completed.State != api.OperationSucceeded || completed.PlanLimits != fresh.PlanLimits {
		t.Fatalf("plan snapshot lost: %+v %v", completed, err)
	}
}

func TestMemOperationRetentionCancellation(t *testing.T) {
	testOperationRetentionCancellation(t, state.NewMemStore())
}
func TestPgOperationRetentionCancellation(t *testing.T) {
	s, _ := pgStore(t)
	testOperationRetentionCancellation(t, s)
}

func TestPgOperationAdmissionRollback(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	s := state.NewPgStore(pool)
	_, acct, _, def, alice, _ := operationFixture(t, s)
	_, err := pool.Exec(ctx, `CREATE FUNCTION reject_operation_write() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected operation write failure'; END $$;
CREATE TRIGGER reject_operation_write BEFORE INSERT ON customer_operations FOR EACH ROW EXECUTE FUNCTION reject_operation_write()`)
	if err != nil {
		t.Fatal(err)
	}
	admission := state.OperationAdmission{AccountID: acct.ID, PlatformTenantID: alice.ID, DefinitionID: def.ID, IdempotencyKey: "rollback", Input: json.RawMessage(`{"count":1}`)}
	if _, _, err := s.AdmitOperation(ctx, admission); err == nil {
		t.Fatal("injected admission failure was ignored")
	}
	var invocations, receipts int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM invocations WHERE account_id=$1), (SELECT count(*) FROM customer_operation_idempotency WHERE account_id=$1)`, acct.ID).Scan(&invocations, &receipts); err != nil {
		t.Fatal(err)
	}
	if invocations != 0 || receipts != 0 {
		t.Fatalf("partial admission committed: %d executions, %d receipts", invocations, receipts)
	}
	if _, err := pool.Exec(ctx, `DROP TRIGGER reject_operation_write ON customer_operations`); err != nil {
		t.Fatal(err)
	}
	if _, created, err := s.AdmitOperation(ctx, admission); err != nil || !created {
		t.Fatalf("retry after rolled-back admission: %v %v", created, err)
	}
}
