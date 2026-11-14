package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type deployRateReserveTestStore interface {
	ConsumeAccountDeployRate(context.Context, string, int, time.Time) (state.AccountDeployRateSnapshot, error)
	ReserveAccountDeployRate(context.Context, string, int, int, time.Time) (state.AccountDeployRateSnapshot, error)
}

// Deploy-path review P17: a project deploy reserves one deploy per service,
// all or none, so a window that cannot fit every service deploys none.
func runDeployRateReserveScenario(t *testing.T, ctx context.Context, s deployRateReserveTestStore, accountID string) {
	t.Helper()
	now := time.Now().UTC()
	if snap, err := s.ConsumeAccountDeployRate(ctx, accountID, 5, now); err != nil || !snap.Allowed {
		t.Fatalf("ConsumeAccountDeployRate = %+v, %v", snap, err)
	}
	snap, err := s.ReserveAccountDeployRate(ctx, accountID, 5, 3, now)
	if err != nil || !snap.Allowed || snap.Used != 4 || snap.Remaining != 1 {
		t.Fatalf("reserve 3 of 4 left = %+v, %v; want allowed, used 4", snap, err)
	}
	snap, err = s.ReserveAccountDeployRate(ctx, accountID, 5, 2, now)
	if err != nil || snap.Allowed || snap.Used != 4 {
		t.Fatalf("reserve 2 of 1 left = %+v, %v; want refused with nothing taken", snap, err)
	}
	if _, err := s.ReserveAccountDeployRate(ctx, accountID, 5, 0, now); err == nil {
		t.Fatal("reserve 0 deploys succeeded")
	}
	snap, err = s.ReserveAccountDeployRate(ctx, accountID, 5, 2, now.Add(state.AccountDeployRateWindow))
	if err != nil || !snap.Allowed || snap.Used != 2 {
		t.Fatalf("reserve in the next window = %+v, %v; want allowed, used 2", snap, err)
	}
}

func TestMem_ReserveAccountDeployRateIsAllOrNothing(t *testing.T) {
	m := state.NewMemStore()
	ctx := context.Background()
	acct, err := m.CreateAccount(ctx, "reserve-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	runDeployRateReserveScenario(t, ctx, m, acct.ID)
}

func TestPg_ReserveAccountDeployRateIsAllOrNothing(t *testing.T) {
	s, ctx := pgStore(t)
	acct, _ := seedPgAccountAndApp(t, s, ctx)
	runDeployRateReserveScenario(t, ctx, s, acct.ID)
}
