package runtimeupgrade

// adr: 691

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type supervisedStore struct {
	state.RuntimeUpgradeOperationStore
	attempts []time.Time
	advanced int
}

func (s *supervisedStore) ClaimRuntimeUpgradeOperation(context.Context) (state.RuntimeUpgradeOperationClaim, error) {
	s.attempts = append(s.attempts, time.Now())
	switch len(s.attempts) {
	case 1, 2, 3, 4, 5:
		return state.RuntimeUpgradeOperationClaim{}, errors.New("synthetic database outage")
	case 6:
		return state.RuntimeUpgradeOperationClaim{ID: "operation", LeaseToken: "new-token"}, nil
	case 7:
		return state.RuntimeUpgradeOperationClaim{}, state.ErrNotFound
	default:
		return state.RuntimeUpgradeOperationClaim{}, errors.New("synthetic database outage")
	}
}

func (s *supervisedStore) AdvanceRuntimeUpgradeOperation(context.Context, state.RuntimeUpgradeOperationClaim) (state.RuntimeUpgradeOperation, error) {
	s.advanced++
	return state.RuntimeUpgradeOperation{}, errors.New("synthetic lost advance response")
}

func TestExecutorSupervisionBackoffRecoveryAndCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		store := &supervisedStore{}
		observed := 0
		executor := Executor{Store: store, Observe: func(error) {
			observed++
			if observed == 8 {
				cancel()
			}
		}}
		if err := executor.Run(ctx); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		want := []time.Duration{5, 10, 20, 30, 30, 30, 5}
		if len(store.attempts) != 8 || store.advanced != 1 {
			t.Fatal(store.attempts, store.advanced)
		}
		for i, seconds := range want {
			if gap := store.attempts[i+1].Sub(store.attempts[i]); gap != seconds*time.Second {
				t.Fatalf("gap %d = %v", i, gap)
			}
		}
	})
}

func TestExecutorStopsDuringBackoffAndBoundsHungStep(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		store := &supervisedStore{}
		done := make(chan error, 1)
		go func() { done <- (Executor{Store: store}).Run(ctx) }()
		synctest.Wait()
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) || len(store.attempts) != 1 {
			t.Fatal(err, store.attempts)
		}
		start := time.Now()
		if _, err := (Executor{Store: hungClaimStore{}}).RunOnce(t.Context()); !errors.Is(err, context.DeadlineExceeded) || time.Since(start) != api.RuntimeUpgradeOperationLease {
			t.Fatal(err)
		}
	})
}

type hungClaimStore struct {
	state.RuntimeUpgradeOperationStore
}

func (hungClaimStore) ClaimRuntimeUpgradeOperation(ctx context.Context) (state.RuntimeUpgradeOperationClaim, error) {
	<-ctx.Done()
	return state.RuntimeUpgradeOperationClaim{}, ctx.Err()
}
