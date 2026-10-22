// adr: 20261022155812
package sched

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func claimedPullWorkerGraphFixture(t *testing.T) (*state.MemStore, []state.EnvironmentWorkloadQualificationRequest) {
	t.Helper()
	enabled := true
	store, _, requests := queuedQualificationExecutionFixtureWithWorkloads(t, nil, "", func(workloads map[string]api.EnvironmentWorkload) {
		worker := workloads["api"]
		worker.QueueBindings = map[string]api.EnvironmentQueueBinding{"orders": {QueueName: "orders", Mode: "pull", WorkloadClass: "worker", Enabled: &enabled}}
		worker.QueueSmoke = map[string]api.EnvironmentQueueSmoke{"orders": {Payload: json.RawMessage(`{"id":"smoke"}`)}}
		workloads["api"] = worker
	}, api.ExecutionModeWorker)
	engine := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "test-fc")
	claimed, err := store.ClaimEnvironmentWorkloadQualificationGraphForNode(t.Context(), requests[0].GraphID, engine.defaultLocalNodeID, "scheduler", time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("pull worker graph claim: %+v %v", claimed, err)
	}
	return store, claimed
}

// leasePullSmoke is the candidate's own consumer: it long-polls the store the
// way the node-local proxy does until the armed message appears.
func leasePullSmoke(t *testing.T, ctx context.Context, store *state.MemStore, caller state.EnvironmentQualificationPullCaller) state.EnvironmentQualificationPullLease {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		leases, err := store.LeaseEnvironmentQualificationPullSmoke(ctx, caller, 30*time.Second)
		if err != nil {
			t.Fatalf("lease: %v", err)
		}
		if len(leases) == 1 {
			return leases[0]
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("pull smoke was never armed")
	return state.EnvironmentQualificationPullLease{}
}

func TestEnvironmentQualificationPullWorkerLeasesItsReviewedSmoke(t *testing.T) {
	for _, outcome := range []string{state.PullSmokeAck, state.PullSmokeNack} {
		t.Run(outcome, func(t *testing.T) {
			store, requests := claimedPullWorkerGraphFixture(t)
			request := requests[0]
			v := &qualificationGraphVMM{qualificationRuntimeVMM: newQualificationRuntimeVMM(&fakeVMM{}), callerID: request.AppID}
			e := newEngine(t, store, v, &fakeNotifier{}, "test-fc").WithEnvironmentQualificationServiceProxy(func(context.Context, string) (string, error) { return "http://10.100.0.1:10081", nil })
			app, err := store.AppByID(t.Context(), request.AppID)
			if err != nil {
				t.Fatal(err)
			}
			err = e.WithEnvironmentQualificationGraphRuntimes(t.Context(), requests, func(ctx context.Context, instances map[string]state.Instance) error {
				worker := instances["workload/api"]
				leaseURL := ""
				for _, env := range v.callerSpec.APIEnv {
					if env.Key == api.QueueLeaseURLEnv {
						leaseURL = env.Value
					}
				}
				if leaseURL != "http://10.100.0.1:10081"+api.EnvironmentQualificationQueuePrefix {
					t.Fatalf("lease URL = %q", leaseURL)
				}
				caller := state.EnvironmentQualificationPullCaller{NodeID: worker.NodeID, HostIP: worker.HostIP, App: app.Slug, Queue: "orders"}
				if leases, err := store.LeaseEnvironmentQualificationPullSmoke(ctx, caller, 30*time.Second); err != nil || len(leases) != 0 {
					t.Fatalf("unarmed lease = %+v %v", leases, err)
				}
				for _, bad := range []state.EnvironmentQualificationPullCaller{
					{NodeID: worker.NodeID, HostIP: worker.HostIP, App: app.Slug, Queue: "payments"},
					{NodeID: worker.NodeID, HostIP: worker.HostIP, App: "other-app", Queue: "orders"},
				} {
					if _, err := store.LeaseEnvironmentQualificationPullSmoke(ctx, bad, 30*time.Second); !errors.Is(err, state.ErrNotFound) {
						t.Fatalf("lease %+v err = %v, want not found", bad, err)
					}
				}
				stranger := caller
				stranger.HostIP = "10.100.0.250"
				if _, err := store.LeaseEnvironmentQualificationPullSmoke(ctx, stranger, 30*time.Second); !errors.Is(err, state.ErrConflict) {
					t.Fatalf("unknown address err = %v, want conflict", err)
				}

				probed := make(chan error, 1)
				go func() { probed <- e.ProbeEnvironmentQualificationSourceGraph(ctx, instances) }()
				lease := leasePullSmoke(t, ctx, store, caller)
				binding := request.FrozenInputs.QueueBindings["orders"]
				if lease.MessageID != qualificationQueueSmokeMessageID(request, binding.BindingID) || string(lease.Payload) != `{"id":"smoke"}` || lease.Attempt != 1 {
					t.Fatalf("leased smoke = %+v", lease)
				}
				if leases, err := store.LeaseEnvironmentQualificationPullSmoke(ctx, caller, 30*time.Second); err != nil || len(leases) != 0 {
					t.Fatalf("held message leased twice: %+v %v", leases, err)
				}
				out, err := store.SettleEnvironmentQualificationPullSmoke(ctx, caller, state.PullSmokeExtend, []string{lease.Receipt, "forged"}, 30*time.Second)
				if err != nil || len(out.Succeeded) != 1 || len(out.Failed) != 1 || out.Failed[0].Reason != state.QueueLeaseReasonInvalid {
					t.Fatalf("extend = %+v %v", out, err)
				}
				if out, err := store.SettleEnvironmentQualificationPullSmoke(ctx, caller, outcome, []string{lease.Receipt}, 0); err != nil || len(out.Succeeded) != 1 {
					t.Fatalf("%s = %+v %v", outcome, out, err)
				}
				if out, err := store.SettleEnvironmentQualificationPullSmoke(ctx, caller, state.PullSmokeAck, []string{lease.Receipt}, 0); err != nil || len(out.Failed) != 1 || out.Failed[0].Reason != state.QueueLeaseReasonExpired {
					t.Fatalf("settled twice: %+v %v", out, err)
				}
				return <-probed
			})
			if outcome == state.PullSmokeAck && err != nil {
				t.Fatalf("acked pull smoke failed the source probe: %v", err)
			}
			if outcome == state.PullSmokeNack && !errors.Is(err, state.ErrConflict) {
				t.Fatalf("nacked pull smoke err = %v, want conflict", err)
			}
		})
	}
}

func TestEnvironmentQualificationPullWorkerNeedsTheNodeLocalProxy(t *testing.T) {
	store, requests := claimedPullWorkerGraphFixture(t)
	v := &qualificationGraphVMM{qualificationRuntimeVMM: newQualificationRuntimeVMM(&fakeVMM{}), callerID: requests[0].AppID}
	e := newEngine(t, store, v, &fakeNotifier{}, "test-fc")
	err := e.WithEnvironmentQualificationGraphRuntimes(t.Context(), requests, func(context.Context, map[string]state.Instance) error {
		t.Fatal("pull worker booted without a lease route")
		return nil
	})
	if !errors.Is(err, state.ErrEnvironmentWorkloadPreparationUnavailable) || len(v.boots) != 0 {
		t.Fatalf("err = %v boots = %v", err, v.boots)
	}
}
