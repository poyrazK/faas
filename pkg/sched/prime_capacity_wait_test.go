package sched_test

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// A built deployment refused at snapshot prime because no node has room waits
// for prime recovery inside api.SnapshotPrimeCapacityMaxWait instead of
// failing; other failures, and refusals past the window, still fail it.
func TestFinishFailedPrime_WaitsForNodeCapacity(t *testing.T) {
	vcpus := int64(4)
	refusal := sched.NodeBudgetRefusal("placement: no compute node fits",
		api.WorkerAdmissionBlocker{Code: "node_vcpu", Resource: "vcpu", Requested: &vcpus})
	tests := []struct {
		name     string
		err      error
		elapsed  time.Duration
		wantCode string // "" means the deployment keeps waiting in snapshotting
	}{
		{name: "capacity refusal inside the window waits", err: refusal, elapsed: time.Minute},
		{name: "capacity refusal past the window fails", err: refusal, elapsed: api.SnapshotPrimeCapacityMaxWait + time.Second, wantCode: api.CodeCapacity},
		{name: "other failure fails at once", err: errors.New("sched: prime: cold boot: guest exited"), elapsed: time.Minute, wantCode: api.CodeDeployFailed},
	}
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := rolloutRecoveryStore(t, backend)
			for _, tc := range tests {
				t.Run(tc.name, func(t *testing.T) {
					ctx := t.Context()
					app, d := primeAttemptWorker(t, s)
					e := sched.XNewEngine(t, s, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
					*e.Now() = func() time.Time { return time.Now().Add(tc.elapsed) }
					loop := sched.NewLoop(nil, e, sched.TestLog())

					loop.FinishFailedPrime(ctx, app.ID, d.ID, tc.err)

					got, err := s.DeploymentByID(ctx, d.ID)
					if err != nil {
						t.Fatal(err)
					}
					if tc.wantCode == "" {
						if got.Status != state.DeploySnapshotting || got.ErrorCode != "" {
							t.Fatalf("deployment = %s/%q, want snapshotting awaiting capacity", got.Status, got.ErrorCode)
						}
						return
					}
					if got.Status != state.DeployFailed || got.ErrorCode != tc.wantCode {
						t.Fatalf("deployment = %s/%q, want failed/%q", got.Status, got.ErrorCode, tc.wantCode)
					}
				})
			}
		})
	}
}
