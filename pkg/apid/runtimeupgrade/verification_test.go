package runtimeupgrade

// adr: 694

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type verificationRetryStore struct {
	state.RuntimeUpgradeVerificationJournalStore
	claims, advances int
}

func (s *verificationRetryStore) ClaimRuntimeUpgradeVerification(context.Context) (state.RuntimeUpgradeOperationClaim, error) {
	s.claims++
	if s.claims == 2 {
		return state.RuntimeUpgradeOperationClaim{}, state.ErrNotFound
	}
	return state.RuntimeUpgradeOperationClaim{ID: "operation", LeaseToken: "fresh"}, nil
}

func (s *verificationRetryStore) AdvanceRuntimeUpgradeVerification(context.Context, state.RuntimeUpgradeOperationClaim) (state.RuntimeUpgradeVerificationJournal, error) {
	s.advances++
	if s.advances == 1 {
		return state.RuntimeUpgradeVerificationJournal{}, errors.New("synthetic lost checkpoint response")
	}
	return state.RuntimeUpgradeVerificationJournal{}, nil
}

type idleActivationStore struct {
	state.RuntimeUpgradeOperationStore
}

func (idleActivationStore) ClaimRuntimeUpgradeOperation(context.Context) (state.RuntimeUpgradeOperationClaim, error) {
	return state.RuntimeUpgradeOperationClaim{}, errors.New("synthetic activation outage")
}

func TestVerificationContinuesDuringActivationFailureAndRetriesLostResponse(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		verification := &verificationRetryStore{}
		observed := 0
		executor := Executor{Store: idleActivationStore{}, VerificationStore: verification, Observe: func(err error) {
			if err == nil {
				t.Fatal("activation failure was hidden")
			}
			observed++
			if observed == 3 {
				cancel()
			}
		}}
		if err := executor.Run(ctx); !errors.Is(err, context.Canceled) || verification.claims != 3 || verification.advances != 2 {
			t.Fatal(err, verification.claims, verification.advances)
		}
	})
}

type hungVerificationStore struct {
	state.RuntimeUpgradeVerificationJournalStore
}

func (hungVerificationStore) ClaimRuntimeUpgradeVerification(ctx context.Context) (state.RuntimeUpgradeOperationClaim, error) {
	<-ctx.Done()
	return state.RuntimeUpgradeOperationClaim{}, ctx.Err()
}

func TestVerificationBoundsHungClaimAndCancels(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		if _, err := (VerificationExecutor{Store: hungVerificationStore{}}).RunOnce(t.Context()); !errors.Is(err, context.DeadlineExceeded) || time.Since(start) != api.RuntimeUpgradeOperationLease {
			t.Fatal(err, time.Since(start))
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := (VerificationExecutor{Store: hungVerificationStore{}}).RunOnce(ctx); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	})
}

func TestVerificationStatusClearsInternalLeaseAndSchedulingClocks(t *testing.T) {
	j := safeVerificationJournal(state.RuntimeUpgradeVerificationJournal{LeaseToken: "private", LeaseUntil: time.Now(), NextAttemptAt: time.Now()})
	if j.LeaseToken != "" || !j.LeaseUntil.IsZero() || !j.NextAttemptAt.IsZero() {
		t.Fatal("private status exposed worker internals", j)
	}
}
