//go:build !no_pg

// adr: 20261027163626
package state

import (
	"context"
	"sync"
	"testing"
	"time"
)

type trafficPhaseRecorder struct {
	mu     sync.Mutex
	phases map[[2]string][]time.Duration
}

func recordTrafficPolicyPhases(t *testing.T) *trafficPhaseRecorder {
	t.Helper()
	recorder := &trafficPhaseRecorder{phases: make(map[[2]string][]time.Duration)}
	SetTrafficPolicyPhaseObserver(func(writer, phase string, elapsed time.Duration) {
		recorder.mu.Lock()
		defer recorder.mu.Unlock()
		key := [2]string{writer, phase}
		recorder.phases[key] = append(recorder.phases[key], elapsed)
	})
	t.Cleanup(func() { SetTrafficPolicyPhaseObserver(nil) })
	return recorder
}

func (r *trafficPhaseRecorder) longest(writer, phase string) (time.Duration, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	observed, found := r.phases[[2]string{writer, phase}]
	var longest time.Duration
	for _, elapsed := range observed {
		longest = max(longest, elapsed)
	}
	return longest, found
}

type trafficPhaseCommitter interface {
	Commit(context.Context) error
}

// Every guarded writer records each of its phases, including lock_wait at
// zero contention, under its own closed writer label.
func TestPgTrafficPolicyWritersRecordPhases(t *testing.T) {
	store, _, account, app := trafficHostPGFixture(t)
	recorder := recordTrafficPolicyPhases(t)
	binding := []string{TrafficPolicyPhaseDiscover, TrafficPolicyPhaseLockWait, TrafficPolicyPhaseBefore, TrafficPolicyPhaseValidate}
	policy := []string{TrafficPolicyPhaseLockWait, TrafficPolicyPhaseBefore, TrafficPolicyPhaseValidate}
	for _, tc := range []struct {
		writer string
		phases []string
		begin  func(context.Context) (trafficPhaseCommitter, error)
	}{
		{TrafficPolicyWriterAppBinding, binding, func(ctx context.Context) (trafficPhaseCommitter, error) {
			return store.beginAccountAppTrafficMutation(ctx, account.ID, app.ID)
		}},
		{TrafficPolicyWriterTenantBinding, binding, func(ctx context.Context) (trafficPhaseCommitter, error) {
			return store.beginTrafficTenantBinding(ctx, account.ID, nil)
		}},
		{TrafficPolicyWriterDomainBinding, binding, func(ctx context.Context) (trafficPhaseCommitter, error) {
			return store.beginTrafficDomainPublication(ctx, "phases.example.test", app.ID)
		}},
		{TrafficPolicyWriterPolicy, policy, func(ctx context.Context) (trafficPhaseCommitter, error) {
			return store.beginTrafficPolicyMutation(ctx, uuidToPgtype(account.ID))
		}},
		{TrafficPolicyWriterRouteRule, policy, func(ctx context.Context) (trafficPhaseCommitter, error) {
			return store.beginRuleTrafficPolicyMutation(ctx, uuidToPgtype(account.ID), EdgeRuleKindRoute)
		}},
	} {
		t.Run(tc.writer, func(t *testing.T) {
			tx, err := tc.begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(t.Context()); err != nil {
				t.Fatal(err)
			}
			for _, phase := range tc.phases {
				if _, found := recorder.longest(tc.writer, phase); !found {
					t.Errorf("%s recorded no %s phase", tc.writer, phase)
				}
			}
		})
	}
}

// lock_wait measures the time a binding write spent behind another holder of
// the global route lock, which is the serialization the ADR removes.
func TestPgTrafficBindingLockWaitMeasuresContention(t *testing.T) {
	store, _, account, app := trafficHostPGFixture(t)
	recorder := recordTrafficPolicyPhases(t)
	_, release, busy, err := store.tryAcquireTrafficPolicySessionKeys(t.Context(), []string{globalTrafficRoutesLock})
	if err != nil || busy {
		t.Fatalf("hold global route lock: busy=%v err=%v", busy, err)
	}
	const held = 150 * time.Millisecond
	done := make(chan error, 1)
	go func() {
		tx, err := store.beginAccountAppTrafficMutation(t.Context(), account.ID, app.ID)
		if err == nil {
			err = tx.Rollback(context.WithoutCancel(t.Context()))
		}
		done <- err
	}()
	time.Sleep(held)
	release(t.Context())
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if waited, _ := recorder.longest(TrafficPolicyWriterAppBinding, TrafficPolicyPhaseLockWait); waited < held/2 {
		// The writer starts at about the same time as the hold, so it waits
		// nearly all of it; half allows for goroutine start-up and discovery.
		t.Fatalf("lock_wait = %v, want most of the %v the lock was held", waited, held)
	}
}
