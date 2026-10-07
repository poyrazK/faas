// adr: 662
package acceptance_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func testOperationRecoveryReceipts(t *testing.T, s operationLifecycleStore) {
	t.Helper()
	ctx, account, _, def, tenant, _ := operationFixture(t, s)
	op, _, err := s.AdmitOperation(ctx, state.OperationAdmission{AccountID: account.ID, DefinitionID: def.ID, PlatformTenantID: tenant.ID, IdempotencyKey: "recovery-receipt", Input: []byte(`{"count":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	inv, err := s.ClaimInvocation(ctx, op.CurrentInvocationID, "", 60)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.FailInvocation(ctx, inv.ID, "lost response", time.Second, 10, state.WithClaimAttempt(inv.Attempts)); err != nil {
		t.Fatal(err)
	}
	before, err := s.OperationByID(ctx, account.ID, "", op.ID)
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := s.(state.OperationRecoveryInspectionStore).InspectOperationRecovery(ctx, account.ID, op.ID)
	if err != nil {
		t.Fatal(err)
	}
	req := api.OperationRecoveryRequest{RecoveryID: "checked-42", ExpectedGeneration: 1, Resolution: "safe_to_retry", Evidence: "provider ledger confirms no effect", ExpectedInspectionRevision: inspection.InspectionRevision}
	receipts := s.(state.OperationRecoveryReceiptStore)
	if _, err = receipts.RecoverOperationWithReceipt(ctx, uuid.NewString(), op.ID, req); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("foreign account", err)
	}
	var wg sync.WaitGroup
	results := make(chan api.OperationRecoveryDecision, 24)
	for range 24 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, e := receipts.RecoverOperationWithReceipt(ctx, account.ID, op.ID, req)
			if e != nil {
				t.Errorf("concurrent decision: %v", e)
				return
			}
			results <- d
		}()
	}
	wg.Wait()
	close(results)
	var first api.OperationRecoveryDecision
	for d := range results {
		if first.OperationID == "" {
			first = d
		}
		if d != first {
			t.Fatalf("non-immutable acknowledgement: %+v %+v", first, d)
		}
	}
	if first.OperationID != op.ID || first.ExpectedGeneration != 1 || first.Generation != 2 || first.State != api.OperationAccepted || first.InvocationID == inv.ID || first.WorkflowRunID != "" || !first.ExpiresAt.Equal(before.ExpiresAt) {
		t.Fatalf("decision: %+v", first)
	}
	current, _ := s.OperationByID(ctx, account.ID, "", op.ID)
	if current.RecoveryCount != 1 || current.CurrentInvocationID != first.InvocationID {
		t.Fatal("duplicate recovery execution", current)
	}
	newer, err := s.ClaimInvocation(ctx, current.CurrentInvocationID, "", 60)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.FailInvocation(ctx, newer.ID, "second lost response", time.Second, 10, state.WithClaimAttempt(newer.Attempts)); err != nil {
		t.Fatal(err)
	}
	terminal := api.OperationRecoveryRequest{RecoveryID: "confirmed-result", ExpectedGeneration: 2, Resolution: "succeeded", Evidence: "provider receipt confirms the export", Result: []byte(`{"file":"verified.csv"}`)}
	final, err := receipts.RecoverOperationWithReceipt(ctx, account.ID, op.ID, terminal)
	if err != nil || final.State != api.OperationSucceeded || final.Generation != 2 {
		t.Fatal("terminal decision", final, err)
	}
	replay, err := receipts.RecoverOperationWithReceipt(ctx, account.ID, op.ID, req)
	if err != nil || replay != first {
		t.Fatal("advanced work rewrote original decision", replay, err)
	}
	legacy, err := s.RecoverOperation(ctx, account.ID, "", op.ID, req)
	if err != nil || legacy.State != api.OperationSucceeded || legacy.RecoveryCount != 2 {
		t.Fatal("legacy current response changed", legacy, err)
	}
	changed := req
	changed.Evidence += " changed"
	if _, err = receipts.RecoverOperationWithReceipt(ctx, account.ID, op.ID, changed); !errors.Is(err, state.ErrOperationInputConflict) {
		t.Fatal("changed decision accepted", err)
	}
	changed = req
	changed.RecoveryID = "another"
	if _, err = receipts.RecoverOperationWithReceipt(ctx, account.ID, op.ID, changed); !errors.Is(err, state.ErrConflict) {
		t.Fatal("stale decision repeated work", err)
	}
	history, err := s.OperationEvents(ctx, account.ID, tenant.ID, op.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	recovered := 0
	for _, event := range history.Events {
		if event.Type == "recovery_requested" {
			recovered++
		}
	}
	if recovered != 1 {
		t.Fatal("duplicate recovery event", recovered)
	}
}

func TestMemOperationRecoveryReceipts(t *testing.T) {
	testOperationRecoveryReceipts(t, state.NewMemStore())
}
func TestPgOperationRecoveryReceipts(t *testing.T) {
	s, _ := pgStore(t)
	testOperationRecoveryReceipts(t, s)
}

func TestPgOperationRecoveryReceiptHistoricalRows(t *testing.T) {
	for _, kind := range []string{"legacy", "expired"} {
		t.Run(kind, func(t *testing.T) {
			pool := pgtest.OpenMigrated(t)
			s := state.NewPgStore(pool)
			ctx, account, _, def, tenant, _ := operationFixture(t, s)
			op, _, err := s.AdmitOperation(ctx, state.OperationAdmission{AccountID: account.ID, DefinitionID: def.ID, PlatformTenantID: tenant.ID, IdempotencyKey: "historical-receipt", Input: []byte(`{"count":1}`)})
			if err != nil {
				t.Fatal(err)
			}
			inv, err := s.ClaimInvocation(ctx, op.CurrentInvocationID, "", 60)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.FailInvocation(ctx, inv.ID, "unknown effect", time.Second, 10, state.WithClaimAttempt(inv.Attempts)); err != nil {
				t.Fatal(err)
			}
			req := api.OperationRecoveryRequest{RecoveryID: "checked", ExpectedGeneration: 1, Resolution: "safe_to_retry", Evidence: "verified no effect"}
			if _, err = s.RecoverOperationWithReceipt(ctx, account.ID, op.ID, req); err != nil {
				t.Fatal(err)
			}
			if kind == "legacy" {
				// Simulate an accepted row written by a pre-upgrade control plane.
				_, err = pool.Exec(ctx, `UPDATE customer_operation_recoveries SET decision=NULL WHERE operation_id=$1`, op.ID)
			} else {
				// Later operation retention must not extend an earlier decision's horizon.
				_, err = pool.Exec(ctx, `UPDATE customer_operation_recoveries SET decision=jsonb_set(decision,'{expires_at}',to_jsonb($2::text)) WHERE operation_id=$1`, op.ID, time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano))
			}
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.RecoverOperationWithReceipt(ctx, account.ID, op.ID, req)
			want := state.ErrOperationRecoveryReceiptUnavailable
			if kind == "expired" {
				want = state.ErrOperationExpired
			}
			if !errors.Is(err, want) {
				t.Fatal("historical receipt replay", kind, err)
			}
			legacy, err := s.RecoverOperation(ctx, account.ID, "", op.ID, req)
			if err != nil || legacy.Generation != 2 || legacy.RecoveryCount != 1 {
				t.Fatal("receipt error repeated work", legacy, err)
			}
		})
	}
}
