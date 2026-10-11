// adr: 521 — losing a claim cancels dispatch without repeating uncertain effects.
package sched

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type lostOperationLeaseStore struct {
	*state.MemStore
	err error
}

func (s *lostOperationLeaseStore) RenewOperationExecution(context.Context, string, int, int) (bool, error) {
	return false, s.err
}

type interruptedOperationHandler struct{ calls atomic.Int64 }

func (*interruptedOperationHandler) SynthesizeRequest(context.Context, string, string, string) error {
	return nil
}

func (h *interruptedOperationHandler) Invoke(ctx context.Context, _ string, inv state.Invocation) (state.Invocation, error) {
	h.calls.Add(1)
	<-ctx.Done()
	return inv, ctx.Err()
}

func TestDrainOperationLeaseLossRequiresReconciliation(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{{"claim-lost", nil}, {"database-unavailable", errors.New("injected database outage")}} {
		t.Run(test.name, func(t *testing.T) {
			store := &lostOperationLeaseStore{MemStore: state.NewMemStore(), err: test.err}
			acct, app, dep := seedApp(t, store, api.PlanPro, 256, 5)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			def, err := store.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: acct.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{
				AppID: app.ID, Scope: dep.Scope, DeploymentID: dep.ID, Spec: api.OperationDefinitionSpec{Name: "export", Method: "POST", Path: "/exports", Owner: api.OperationOwnerPlatformTenant,
					InputSchema: []byte(`{"type":"object"}`), OutputSchema: []byte(`{"type":"object"}`), ProgressStages: []string{"generating"}},
			}})
			if err != nil {
				t.Fatal(err)
			}
			tenant, _, err := store.CreatePlatformTenant(ctx, acct.ID, "customer", "Customer", 100)
			if err != nil {
				t.Fatal(err)
			}
			op, _, err := store.AdmitOperation(ctx, state.OperationAdmission{AccountID: acct.ID, DefinitionID: def.ID, PlatformTenantID: tenant.ID, IdempotencyKey: "export-request", Input: []byte(`{}`)})
			if err != nil {
				t.Fatal(err)
			}
			handler := new(interruptedOperationHandler)
			engine := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
			drain := NewDrain(store, engine, WithDrainGatewaySynth(handler), WithDrainWakeLease(1))
			drain.Tick(ctx)
			current, err := store.OperationByID(ctx, acct.ID, tenant.ID, op.ID)
			if err != nil || current.State != api.OperationRequiresReconciliation || handler.calls.Load() != 1 {
				t.Fatalf("lease loss did not preserve uncertainty: %+v, calls %d, %v", current, handler.calls.Load(), err)
			}
			drain.Tick(ctx)
			if handler.calls.Load() != 1 {
				t.Fatal("uncertain handler was automatically repeated")
			}
		})
	}
}

type rejectedOperationStampStore struct {
	*state.MemStore
	err    error
	reject atomic.Bool
}

func (s *rejectedOperationStampStore) StampOperationExecutionAttempt(ctx context.Context, id, instance string, attempt int) error {
	if s.reject.Load() {
		return s.err
	}
	return s.MemStore.StampOperationExecutionAttempt(ctx, id, instance, attempt)
}

type boundOperationHandler struct {
	store *state.MemStore
	calls atomic.Int64
}

func (*boundOperationHandler) SynthesizeRequest(context.Context, string, string, string) error {
	return nil
}

func (h *boundOperationHandler) Invoke(ctx context.Context, _ string, inv state.Invocation) (state.Invocation, error) {
	h.calls.Add(1)
	persisted, err := h.store.InvocationByID(ctx, inv.ID)
	if err != nil || persisted.InstanceID == "" || persisted.Attempts != inv.Attempts {
		return inv, errors.New("handler reached without persisted execution authority")
	}
	inv.Result = json.RawMessage(`{"generated":true}`)
	return inv, nil
}

func TestDrainOperationInstanceBindingPrecedesHandler(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{{"claim-lost", state.ErrNotFound}, {"database-unavailable", errors.New("injected binding outage")}} {
		t.Run(test.name, func(t *testing.T) {
			store := &rejectedOperationStampStore{MemStore: state.NewMemStore(), err: test.err}
			store.reject.Store(true)
			acct, app, dep := seedApp(t, store, api.PlanPro, 256, 5)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			def, err := store.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: acct.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{
				AppID: app.ID, Scope: dep.Scope, DeploymentID: dep.ID, Spec: api.OperationDefinitionSpec{Name: "export", Method: "POST", Path: "/exports", Owner: api.OperationOwnerPlatformTenant,
					InputSchema: []byte(`{"type":"object"}`), OutputSchema: []byte(`{"type":"object"}`), ProgressStages: []string{"generating"}},
			}})
			if err != nil {
				t.Fatal(err)
			}
			tenant, _, err := store.CreatePlatformTenant(ctx, acct.ID, "customer", "Customer", 100)
			if err != nil {
				t.Fatal(err)
			}
			op, _, err := store.AdmitOperation(ctx, state.OperationAdmission{AccountID: acct.ID, DefinitionID: def.ID, PlatformTenantID: tenant.ID, IdempotencyKey: "export-request", Input: []byte(`{}`)})
			if err != nil {
				t.Fatal(err)
			}
			handler := &boundOperationHandler{store: store.MemStore}
			engine := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
			drain := NewDrain(store, engine, WithDrainGatewaySynth(handler), WithDrainRetryAfter(1))
			drain.Tick(ctx)
			current, err := store.OperationByID(ctx, acct.ID, tenant.ID, op.ID)
			inv, invErr := store.InvocationByID(ctx, op.CurrentInvocationID)
			if err != nil || invErr != nil || current.State != api.OperationAccepted || handler.calls.Load() != 0 || inv.State != state.InvocationPending || inv.QuotaReserved {
				t.Fatalf("binding failure dispatched or stranded work: %+v, invocation %+v, calls %d, errors %v/%v", current, inv, handler.calls.Load(), err, invErr)
			}
			store.reject.Store(false)
			timer := time.NewTimer(time.Until(inv.DueAt) + time.Millisecond)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-timer.C:
			}
			drain.Tick(ctx)
			current, err = store.OperationByID(ctx, acct.ID, tenant.ID, op.ID)
			if err != nil || current.State != api.OperationSucceeded || handler.calls.Load() != 1 || current.Generation != 1 {
				t.Fatalf("retry did not complete the original operation once: %+v, calls %d, %v", current, handler.calls.Load(), err)
			}
		})
	}
}
