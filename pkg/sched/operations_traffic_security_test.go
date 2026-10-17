// adr: 521 — operation claims retain execution authority through delivery.
// adr: 570 — traffic revocation shares that delivery ownership.
package sched

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/trafficrevocation"
)

type operationTrafficLeaseStore struct {
	*state.MemStore
	lose atomic.Bool
}

func (s *operationTrafficLeaseStore) RenewOperationExecution(ctx context.Context, id string, attempt, lease int) (bool, error) {
	if s.lose.Load() {
		return false, nil
	}
	return s.MemStore.RenewOperationExecution(ctx, id, attempt, lease)
}

func TestDrainOperationTrafficSecurityRetainsClaimAndCancellation(t *testing.T) {
	for _, cause := range []string{"lease-loss", "revoked"} {
		t.Run(cause, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
			defer cancel()
			store := &operationTrafficLeaseStore{MemStore: state.NewMemStore()}
			acct, app, dep := seedApp(t, store, api.PlanPro, 256, 5)
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
			op, _, err := store.AdmitOperation(ctx, state.OperationAdmission{AccountID: acct.ID, DefinitionID: def.ID, PlatformTenantID: tenant.ID, IdempotencyKey: "export", Input: []byte(`{}`)})
			if err != nil {
				t.Fatal(err)
			}
			security := &drainSecurityStore{states: make(map[trafficrevocation.Scope]trafficrevocation.State)}
			registry := trafficrevocation.New(security)
			defer registry.Close()
			started, canceled, cleanup, finished := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
			var release sync.Once
			defer release.Do(func() { close(cleanup) })
			var calls atomic.Int64
			handler := &securityDrainSynth{invoke: func(delivery context.Context, row state.Invocation) (state.Invocation, error) {
				calls.Add(1)
				baseline, ok := trafficrevocation.HandoffSnapshot(delivery)
				persisted, loadErr := store.InvocationByID(ctx, row.ID)
				if !ok || len(baseline) != 3 || loadErr != nil || persisted.InstanceID == "" || persisted.Attempts != row.Attempts {
					return row, errors.New("missing operation execution or traffic ownership")
				}
				close(started)
				<-delivery.Done()
				close(canceled)
				<-cleanup
				// A late successful response cannot publish after either fence ends.
				row.Result = json.RawMessage(`{"late":true}`)
				return row, nil
			}}
			engine := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
			drain := NewDrain(store, engine, WithDrainGatewaySynth(handler), WithDrainWakeLease(1), WithDrainTrafficRevocations(registry))
			go func() { defer close(finished); drain.Tick(ctx) }()
			defer func() {
				cancel()
				release.Do(func() { close(cleanup) })
				select {
				case <-finished:
				case <-time.After(2 * time.Second):
					t.Error("operation test cleanup left a dispatch running")
				}
			}()
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("operation did not reach a bound, traffic-admitted handler")
			}
			if cause == "lease-loss" {
				store.lose.Store(true)
			} else {
				security.change(trafficrevocation.Scope{Kind: "app", ID: app.ID}, trafficrevocation.State{Revision: 1, Revoked: true}, nil)
				if err := registry.Refresh(ctx); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case <-canceled:
			case <-time.After(2 * time.Second):
				t.Fatal("operation delivery ignored claim loss or traffic revocation")
			}
			if requests, scopes := registry.Tracked(); requests == 0 || scopes != 3 {
				t.Fatalf("traffic ownership ended before handler cleanup: %d/%d", requests, scopes)
			}
			release.Do(func() { close(cleanup) })
			select {
			case <-finished:
			case <-time.After(2 * time.Second):
				t.Fatal("operation delivery or claim renewal did not stop")
			}
			current, err := store.OperationByID(ctx, acct.ID, tenant.ID, op.ID)
			inv, invErr := store.InvocationByID(ctx, op.CurrentInvocationID)
			if err != nil || invErr != nil || current.State != api.OperationRequiresReconciliation || len(inv.Result) != 0 || calls.Load() != 1 {
				t.Fatalf("fenced response published or lost uncertainty: operation=%+v invocation=%+v calls=%d errors=%v/%v", current, inv, calls.Load(), err, invErr)
			}
			if requests, scopes := registry.Tracked(); requests != 0 || scopes != 0 {
				t.Fatalf("cleanup leaked traffic ownership: %d/%d", requests, scopes)
			}
			drain.Tick(ctx)
			if calls.Load() != 1 {
				t.Fatal("uncertain operation was repeated automatically")
			}
		})
	}
}
