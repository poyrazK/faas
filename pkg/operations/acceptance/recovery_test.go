package acceptance_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func testOperationRecovery(t *testing.T, s operationLifecycleStore) {
	t.Helper()
	ctx, acct, _, def, alice, bob := operationFixture(t, s)
	admission := state.OperationAdmission{AccountID: acct.ID, DefinitionID: def.ID, PlatformTenantID: alice.ID, IdempotencyKey: "recovery", Input: []byte(`{"count":1}`)}
	op, _, err := s.AdmitOperation(ctx, admission)
	if err != nil {
		t.Fatal(err)
	}
	inv, err := s.ClaimInvocation(ctx, op.CurrentInvocationID, "", 60)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FailInvocation(ctx, inv.ID, "lost response", time.Second, 10, state.WithClaimAttempt(inv.Attempts)); err != nil {
		t.Fatal(err)
	}
	recovery := api.OperationRecoveryRequest{RecoveryID: "reconcile-1", ExpectedGeneration: 1, Resolution: "safe_to_retry", Evidence: "checked export storage and provider ledger; no output or external effect exists"}
	if _, err := s.RecoverOperation(ctx, acct.ID, bob.ID, op.ID, recovery); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-customer recovery allowed: %v", err)
	}
	var wg sync.WaitGroup
	results := make(chan state.Operation, 24)
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := s.RecoverOperation(ctx, acct.ID, alice.ID, op.ID, recovery)
			if err != nil {
				t.Errorf("concurrent recovery: %v", err)
				return
			}
			results <- got
		}()
	}
	wg.Wait()
	close(results)
	var recovered state.Operation
	for got := range results {
		if recovered.ID == "" {
			recovered = got
		}
		if got.ID != op.ID || got.Generation != 2 || got.CurrentInvocationID != recovered.CurrentInvocationID || got.CurrentInvocationID == inv.ID {
			t.Fatalf("recovery duplicated execution or logical operation: %+v", got)
		}
	}
	if recovered.ID == "" {
		t.Fatal("no recovery result")
	}
	repeated, fresh, err := s.AdmitOperation(ctx, admission)
	if err != nil || fresh || repeated.ID != op.ID || repeated.CurrentInvocationID != recovered.CurrentInvocationID {
		t.Fatalf("original submission lost stable identity: %+v %v", repeated, err)
	}
	conflict := recovery
	conflict.Evidence = "different evidence"
	if _, err := s.RecoverOperation(ctx, acct.ID, alice.ID, op.ID, conflict); !errors.Is(err, state.ErrOperationInputConflict) {
		t.Fatalf("recovery ID silently changed intent: %v", err)
	}
	stale := recovery
	stale.RecoveryID = "reconcile-2"
	if _, err := s.RecoverOperation(ctx, acct.ID, alice.ID, op.ID, stale); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale generation authorized another attempt: %v", err)
	}
	if err := s.CompleteKeyedInvocation(ctx, inv.ID, inv.Attempts, []byte(`{"file":"late.csv"}`)); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("old execution overwrote recovered work: %v", err)
	}
	newInv, err := s.ClaimInvocation(ctx, recovered.CurrentInvocationID, "", 60)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FailInvocation(ctx, newInv.ID, "another response lost", time.Second, 10, state.WithClaimAttempt(newInv.Attempts)); err != nil {
		t.Fatal(err)
	}
	confirm := api.OperationRecoveryRequest{RecoveryID: "confirm-success", ExpectedGeneration: 2, Resolution: "succeeded", Evidence: "verified generated file contents and customer ownership", Result: []byte(`{"file":"confirmed.csv"}`)}
	confirmed, err := s.RecoverOperation(ctx, acct.ID, "", op.ID, confirm)
	if err != nil || confirmed.State != api.OperationSucceeded || confirmed.Generation != 2 {
		t.Fatalf("confirmed business success: %+v %v", confirmed, err)
	}
	if retry, err := s.RecoverOperation(ctx, acct.ID, "", op.ID, confirm); err != nil || retry.LatestSequence != confirmed.LatestSequence {
		t.Fatalf("confirmation repeated completion: %+v %v", retry, err)
	}
	page, err := s.OperationEvents(ctx, acct.ID, alice.ID, op.ID, 0, 100)
	if err != nil || len(page.Events) != 7 {
		t.Fatalf("recovery execution history: %+v %v", page, err)
	}
	var recoveryEvents int
	for _, event := range page.Events {
		if event.Type == "recovery_requested" {
			recoveryEvents++
		}
	}
	if recoveryEvents != 1 {
		t.Fatal("duplicate recovery event")
	}
}

func TestMemOperationRecovery(t *testing.T) { testOperationRecovery(t, state.NewMemStore()) }
func TestPgOperationRecovery(t *testing.T)  { s, _ := pgStore(t); testOperationRecovery(t, s) }
