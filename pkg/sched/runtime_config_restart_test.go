package sched_test

// adr: 210

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

func TestRuntimeConfigDrainReadsPhysicalNodeAndRequiresQuietPeriod(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, deployment := sched.SeedApp(t, store, api.PlanPro, 256, 5)
	const physicalNodeID = "compute-old-owner"
	instance, err := store.CreateInstance(ctx, app.ID, deployment.ID, string(state.StateDraining), app.RAMMB, physicalNodeID, "wake-old")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	vmm := &sched.FakeVMM{}
	var mu sync.Mutex
	var calls int
	var lastNodeID string
	(*vmm.StatsHook()) = func(_ context.Context, nodeID string) (*sched.StatsSnapshot, error) {
		mu.Lock()
		calls++
		call := calls
		lastNodeID = nodeID
		mu.Unlock()
		inflight := int64(0)
		if call == 1 {
			inflight = 1
		}
		return &sched.StatsSnapshot{Instances: []sched.VMInstanceStat{{InstanceID: instance.ID, InflightRequests: inflight}}}, nil
	}
	engine := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0")

	started := time.Now()
	if err := engine.WaitForRuntimeConfigInstanceDrain(ctx, app.ID, instance.ID, started.Add(-time.Second)); err != nil {
		t.Fatalf("waitForRuntimeConfigInstanceDrain: %v", err)
	}
	if elapsed := time.Since(started); elapsed < time.Duration(api.ServiceReplicaDrainQuietSeconds)*time.Second {
		t.Fatalf("drain returned after %s; want at least %d seconds of fresh zero samples", elapsed, api.ServiceReplicaDrainQuietSeconds)
	}
	mu.Lock()
	gotCalls, gotNodeID := calls, lastNodeID
	mu.Unlock()
	if gotNodeID != physicalNodeID {
		t.Fatalf("Stats routed to node %q, want physical instance node %q", gotNodeID, physicalNodeID)
	}
	if gotCalls < 3 {
		t.Fatalf("Stats calls = %d, want active observation followed by repeated zero observations", gotCalls)
	}
}

func TestRuntimeConfigDrainDoesNotTreatMissingTelemetryAsZero(t *testing.T) {
	tests := []struct {
		name       string
		stats      func(context.Context, string, string) (*sched.StatsSnapshot, error)
		wantReason sched.RuntimeConfigDrainReason
	}{
		{
			name: "instance omitted",
			stats: func(context.Context, string, string) (*sched.StatsSnapshot, error) {
				return &sched.StatsSnapshot{}, nil
			},
			wantReason: sched.RuntimeConfigDrainReasonTelemetryMissing,
		},
		{
			name: "stats unavailable",
			stats: func(context.Context, string, string) (*sched.StatsSnapshot, error) {
				return nil, errors.New("peer unavailable")
			},
			wantReason: sched.RuntimeConfigDrainReasonTelemetryMissing,
		},
		{
			name: "quiet period not elapsed",
			stats: func(_ context.Context, _, instanceID string) (*sched.StatsSnapshot, error) {
				return &sched.StatsSnapshot{Instances: []sched.VMInstanceStat{{InstanceID: instanceID}}}, nil
			},
			wantReason: sched.RuntimeConfigDrainReasonQuietPeriod,
		},
		{
			name: "requests active",
			stats: func(_ context.Context, _, instanceID string) (*sched.StatsSnapshot, error) {
				return &sched.StatsSnapshot{Instances: []sched.VMInstanceStat{{InstanceID: instanceID, InflightRequests: 1}}}, nil
			},
			wantReason: sched.RuntimeConfigDrainReasonRequestsActive,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			_, app, deployment := sched.SeedApp(t, store, api.PlanPro, 256, 5)
			instance, err := store.CreateInstance(ctx, app.ID, deployment.ID, string(state.StateDraining), app.RAMMB, "compute-old-owner", "wake-old")
			if err != nil {
				t.Fatalf("CreateInstance: %v", err)
			}
			vmm := &sched.FakeVMM{}
			(*vmm.StatsHook()) = func(callCtx context.Context, nodeID string) (*sched.StatsSnapshot, error) {
				return tt.stats(callCtx, nodeID, instance.ID)
			}
			engine := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0")
			waitCtx, cancel := context.WithTimeout(ctx, 350*time.Millisecond)
			defer cancel()
			err = engine.WaitForRuntimeConfigInstanceDrain(waitCtx, app.ID, instance.ID, time.Now().Add(-time.Second))
			if err == nil {
				t.Fatal("waitForRuntimeConfigInstanceDrain succeeded without a sustained, authoritative zero sample")
			}
			if !strings.Contains(err.Error(), "reason="+string(tt.wantReason)) {
				t.Fatalf("drain error %q does not expose reason %q", err, tt.wantReason)
			}
		})
	}
}

// deploymentLookupRecorder records DeploymentByID ids.
type deploymentLookupRecorder struct {
	MemStore *state.MemStore
	ids      []string
}

func (r *deploymentLookupRecorder) DeploymentByID(ctx context.Context, id string) (state.Deployment, error) {
	r.ids = append(r.ids, id)
	return r.MemStore.DeploymentByID(ctx, id)
}

// Production-us hunt #9 (H9-102): the runtime-config restart converges routes
// with an empty rollout id; every handoff phase update looked it up and
// Postgres rejected "" as a uuid.
func TestRuntimeConfigRestartHandoffSkipsEmptyRolloutID(t *testing.T) {
	store := &deploymentLookupRecorder{MemStore: state.NewMemStore()}
	e := sched.XNewEngine(t, statetest.NewMem(store), &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
	e.UpdateServiceRolloutHandoff(t.Context(), "", func(h *state.ServiceRolloutHandoff) { h.LastError = "x" })
	e.FailServiceRolloutHandoff(t.Context(), "", state.ServiceRolloutPhaseRouting, "route_publish_failed", nil)
	if len(store.ids) != 0 {
		t.Fatalf("DeploymentByID called with %q for a rollout-less restart", store.ids)
	}
}
