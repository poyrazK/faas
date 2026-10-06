package acceptance_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type operationStreamFixture interface {
	operationFixtureStore
	state.OperationStreamStore
	state.OperationRetentionStore
	UpdateAccountPlan(context.Context, string, api.Plan) error
}

func testOperationStreams(t *testing.T, s operationStreamFixture) {
	ctx, acct, _, def, alice, bob := operationFixture(t, s)
	op, _, err := s.AdmitOperation(ctx, state.OperationAdmission{AccountID: acct.ID, PlatformTenantID: alice.ID, DefinitionID: def.ID, IdempotencyKey: "stream", Input: []byte(`{"count":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AcquireOperationStream(ctx, acct.ID, bob.ID, op.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-customer subscription: %v", err)
	}
	if err := s.UpdateAccountPlan(ctx, acct.ID, api.PlanHobby); err != nil {
		t.Fatal(err)
	}
	limit := api.MustLimitsFor(api.PlanHobby).Operations.SubscriptionsPerAccount
	var created, denied atomic.Int64
	leases := make(chan string, limit)
	var wg sync.WaitGroup
	for i := 0; i < limit+4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := s.AcquireOperationStream(ctx, acct.ID, alice.ID, op.ID)
			if errors.Is(err, state.ErrOperationQuota) {
				denied.Add(1)
				return
			}
			if err != nil {
				t.Errorf("stream admission: %v", err)
				return
			}
			created.Add(1)
			leases <- id
		}()
	}
	wg.Wait()
	close(leases)
	if created.Load() != int64(limit) || denied.Load() != 4 {
		t.Fatalf("stream quota raced: %d admitted, %d denied", created.Load(), denied.Load())
	}
	metrics, err := s.(state.OperationMetricsStore).OperationMetrics(ctx, time.Now())
	if err != nil || metrics.ActiveStreams != int64(limit) || len(metrics.States) != 1 || metrics.States[0].State != api.OperationAccepted || metrics.States[0].Count != 1 {
		t.Fatalf("durable health snapshot: %+v %v", metrics, err)
	}
	first := <-leases
	if err := s.RenewOperationStream(ctx, bob.ID, first); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("other principal renewed stream: %v", err)
	}
	if err := s.RenewOperationStream(ctx, acct.ID, first); err != nil {
		t.Fatal(err)
	}
	if err := s.ReleaseOperationStream(ctx, first); err != nil {
		t.Fatal(err)
	}
	replacement, err := s.AcquireOperationStream(ctx, acct.ID, alice.ID, op.ID)
	if err != nil {
		t.Fatalf("closed stream retained quota: %v", err)
	}
	if _, err := s.PruneOperationState(ctx, time.Now().Add(api.OperationStreamLease+time.Second), api.OperationRetentionPageMax); err != nil {
		t.Fatal(err)
	}
	if err := s.RenewOperationStream(ctx, acct.ID, replacement); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("expired lease was resurrected: %v", err)
	}
	if _, err := s.AcquireOperationStream(ctx, acct.ID, alice.ID, op.ID); err != nil {
		t.Fatalf("abandoned streams exhausted quota: %v", err)
	}
}

func TestMemOperationStreams(t *testing.T) { testOperationStreams(t, state.NewMemStore()) }
func TestPgOperationStreams(t *testing.T)  { s, _ := pgStore(t); testOperationStreams(t, s) }
