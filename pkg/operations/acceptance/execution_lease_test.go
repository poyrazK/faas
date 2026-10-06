// ADR-521: a healthy handler retains its lease without reviving abandoned work.
package acceptance_test

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type operationLeaseFixtureStore interface {
	operationLifecycleStore
	state.OperationExecutionLeaseStore
}

func testOperationExecutionLease(t *testing.T, store operationLeaseFixtureStore) {
	t.Helper()
	ctx, acct, _, def, tenant, _ := operationFixture(t, store)
	op, _, err := store.AdmitOperation(ctx, state.OperationAdmission{AccountID: acct.ID, DefinitionID: def.ID, PlatformTenantID: tenant.ID, IdempotencyKey: "healthy-handler", Input: []byte(`{"count":100}`)})
	if err != nil {
		t.Fatal(err)
	}
	inv, err := store.ClaimInvocation(ctx, op.CurrentInvocationID, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if renewed, err := store.RenewOperationExecution(ctx, inv.ID, inv.Attempts+1, 60); err != nil || renewed {
		t.Fatalf("stale attempt renewed claim: %v %v", renewed, err)
	}
	if renewed, err := store.RenewOperationExecution(ctx, inv.ID, inv.Attempts, 60); err != nil || !renewed {
		t.Fatalf("healthy execution was not renewed: %v %v", renewed, err)
	}
	current, err := store.InvocationByID(ctx, inv.ID)
	if err != nil || !current.LeaseExpiresAt.After(*inv.LeaseExpiresAt) || current.LeaseExpiresAt.After(*current.DeadlineAt) {
		t.Fatalf("renewal did not preserve deadline: %+v %v", current, err)
	}
	if reclaimed, err := store.RequeueExpiredInvocations(ctx, inv.LeaseExpiresAt.Add(time.Millisecond), 10); err != nil || reclaimed != 0 {
		t.Fatalf("live operation was reclaimed: %d %v", reclaimed, err)
	}
	if _, err := store.RequeueExpiredInvocations(ctx, current.LeaseExpiresAt.Add(time.Millisecond), 10); err != nil {
		t.Fatal(err)
	}
	if renewed, err := store.RenewOperationExecution(ctx, inv.ID, inv.Attempts, 60); err != nil || renewed {
		t.Fatalf("abandoned execution was revived: %v %v", renewed, err)
	}
	got, err := store.OperationByID(ctx, acct.ID, tenant.ID, op.ID)
	if err != nil || got.State != api.OperationRequiresReconciliation {
		t.Fatalf("abandoned work lost uncertainty: %+v %v", got, err)
	}
	if _, err := store.RenewOperationExecution(ctx, inv.ID, inv.Attempts, int(api.OperationExecutionLeaseMax/time.Second)+1); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("unbounded lease accepted: %v", err)
	}
}

func TestMemOperationExecutionLease(t *testing.T) {
	testOperationExecutionLease(t, state.NewMemStore())
}

func TestPgOperationExecutionLease(t *testing.T) {
	store, _ := pgStore(t)
	testOperationExecutionLease(t, store)
}
