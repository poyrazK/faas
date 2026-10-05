// adr: 521
package acceptance_test

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func testOperationActiveIdentity(t *testing.T, s operationRetentionFixture) {
	t.Helper()
	ctx, acct, _, def, alice, _ := operationFixture(t, s)
	admission := state.OperationAdmission{AccountID: acct.ID, DefinitionID: def.ID, PlatformTenantID: alice.ID,
		IdempotencyKey: "long-running", Input: []byte(`{"count":1}`)}
	op, _, err := s.AdmitOperation(ctx, admission)
	if err != nil {
		t.Fatal(err)
	}
	// Advancing GC past every admitted window must keep an active operation's
	// receipt. Otherwise the same customer submission can enqueue duplicate work.
	cutoff := op.CreatedAt.Add(time.Duration(op.PlanLimits.IdempotencyRetentionSeconds+op.PlanLimits.ResultRetentionSeconds+1) * time.Second)
	if n, err := s.PruneOperationState(ctx, cutoff, api.OperationRetentionPageMax); err != nil || n != 0 {
		t.Fatalf("pruned active operation: %d %v", n, err)
	}
	repeated, fresh, err := s.AdmitOperation(ctx, admission)
	if err != nil || fresh || repeated.ID != op.ID || repeated.CurrentInvocationID != op.CurrentInvocationID {
		t.Fatalf("active receipt expired: %+v %v %v", repeated, fresh, err)
	}
	changed := admission
	changed.Input = []byte(`{"count":2}`)
	if _, _, err := s.AdmitOperation(ctx, changed); !errors.Is(err, state.ErrOperationInputConflict) {
		t.Fatalf("expired active receipt lost payload conflict: %v", err)
	}
	if _, err := s.CancelOperation(ctx, acct.ID, alice.ID, op.ID, op.Generation); err != nil {
		t.Fatal(err)
	}
	if n, err := s.PruneOperationState(ctx, cutoff, api.OperationRetentionPageMax); err != nil || n != 1 {
		t.Fatalf("settled operation did not release retention: %d %v", n, err)
	}
}

func TestMemOperationActiveIdentity(t *testing.T) {
	testOperationActiveIdentity(t, state.NewMemStore())
}

func TestPgOperationActiveIdentity(t *testing.T) {
	s, _ := pgStore(t)
	testOperationActiveIdentity(t, s)
}

func TestPgOperationActivePastRetention(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	s := state.NewPgStore(pool)
	ctx, acct, _, def, alice, _ := operationFixture(t, s)
	admission := state.OperationAdmission{AccountID: acct.ID, DefinitionID: def.ID, PlatformTenantID: alice.ID,
		IdempotencyKey: "past-retention", Input: []byte(`{"count":1}`)}
	op, _, err := s.AdmitOperation(ctx, admission)
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour).UTC()
	if _, err := pool.Exec(ctx, `UPDATE customer_operations SET expires_at=$2,
 record=jsonb_set(jsonb_set(record,'{expires_at}',to_jsonb($2::timestamptz)),'{event_expires_at}',to_jsonb($2::timestamptz)) WHERE id=$1`, op.ID, past); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE customer_operation_idempotency SET expires_at=$2 WHERE operation_id=$1`, op.ID, past); err != nil {
		t.Fatal(err)
	}
	repeated, fresh, err := s.AdmitOperation(ctx, admission)
	if err != nil || fresh || repeated.ID != op.ID {
		t.Fatalf("long-running submission re-executed: %+v %v %v", repeated, fresh, err)
	}
	if n, err := s.PruneOperationState(ctx, time.Now(), api.OperationRetentionPageMax); err != nil || n != 0 {
		t.Fatalf("active state GC: %d %v", n, err)
	}
	if read, err := s.OperationByID(ctx, acct.ID, alice.ID, op.ID); err != nil || read.State != api.OperationAccepted {
		t.Fatalf("active status expired: %+v %v", read, err)
	}
	if page, err := s.OperationEvents(ctx, acct.ID, alice.ID, op.ID, 0, 10); err != nil || !page.ResyncRequired {
		t.Fatalf("expired events did not permit snapshot resynchronization: %+v %v", page, err)
	}
	lease, err := s.AcquireOperationStream(ctx, acct.ID, alice.ID, op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReleaseOperationStream(ctx, lease); err != nil {
		t.Fatal(err)
	}
	if metrics, err := s.OperationMetrics(ctx, time.Now()); err != nil || len(metrics.States) != 1 || metrics.States[0].Count != 1 {
		t.Fatalf("active operation disappeared from health: %+v %v", metrics, err)
	}
	before := time.Now()
	settled, err := s.CancelOperation(ctx, acct.ID, alice.ID, op.ID, op.Generation)
	if err != nil || settled.State != api.OperationCancelled || !settled.ExpiresAt.After(before) {
		t.Fatalf("active operation could not settle: %+v %v", settled, err)
	}
	var retainedUntil time.Time
	if err := pool.QueryRow(ctx, `SELECT expires_at FROM customer_operation_idempotency WHERE operation_id=$1`, op.ID).Scan(&retainedUntil); err != nil {
		t.Fatal(err)
	}
	minimum := before.Add(time.Duration(op.PlanLimits.IdempotencyRetentionSeconds) * time.Second)
	if retainedUntil.Before(minimum) {
		t.Fatalf("settlement did not retain the full identity window: %s < %s", retainedUntil, minimum)
	}
	if n, err := s.PruneOperationState(ctx, settled.ExpiresAt.Add(time.Second), api.OperationRetentionPageMax); err != nil || n != 1 {
		t.Fatalf("settled projection did not expire: %d %v", n, err)
	}
	if _, _, err := s.AdmitOperation(ctx, admission); !errors.Is(err, state.ErrOperationExpired) {
		t.Fatalf("retained tombstone regenerated business work: %v", err)
	}
}
