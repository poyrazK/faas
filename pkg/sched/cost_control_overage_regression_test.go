package sched_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type costControlCapReadFailure struct {
	Store state.Store
	fail  bool
}

func (s *costControlCapReadFailure) GetAccountOverageCapCents(ctx context.Context, id string) (int64, bool, error) {
	if s.fail {
		return 0, false, errors.New("temporary cap-store outage")
	}
	return s.Store.GetAccountOverageCapCents(ctx, id)
}

func TestCostControlReadFailurePreservesKnownSpendCapHold(t *testing.T) {
	mem := state.NewMemStore()
	a, err := mem.CreateAccount(t.Context(), "cost-hold@example.test", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	mem.SetOverageCapCentsForTest(a.ID, 0)
	fake := &costControlCapReadFailure{Store: mem}
	checker := sched.NewMemCacheOverageChecker(statetest.NewStore(fake), time.Nanosecond)
	if status, _, _, err := checker.Check(t.Context(), a.ID); err != nil || status != sched.OverageReached {
		t.Fatalf("initial hold=%v %v", status, err)
	}
	checker.RecordReached(t.Context(), a.ID, 0, 0) // the real admission path also emits its audit event
	fake.fail = true
	if status, _, _, err := checker.Check(t.Context(), a.ID); err == nil || status != sched.OverageReached {
		t.Fatalf("outage released known hold: %v %v", status, err)
	}
	fake.fail = false
	mem.SetOverageCapCentsForTest(a.ID, 100)
	if status, _, _, err := checker.Check(t.Context(), a.ID); err != nil || status != sched.OverageOK {
		t.Fatalf("successful cap raise did not recover: %v %v", status, err)
	}
}
