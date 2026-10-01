package sched

// adr: 210

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRuntimeConfigDrainReadsPhysicalNodeAndRequiresQuietPeriod(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, deployment := seedApp(t, store, api.PlanPro, 256, 5)
	const physicalNodeID = "compute-old-owner"
	instance, err := store.CreateInstance(ctx, app.ID, deployment.ID, string(state.StateDraining), app.RAMMB, physicalNodeID, "wake-old")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	vmm := &fakeVMM{}
	var mu sync.Mutex
	var calls int
	var lastNodeID string
	vmm.statsHook = func(_ context.Context, nodeID string) (*StatsSnapshot, error) {
		mu.Lock()
		calls++
		call := calls
		lastNodeID = nodeID
		mu.Unlock()
		inflight := int64(0)
		if call == 1 {
			inflight = 1
		}
		return &StatsSnapshot{Instances: []VMInstanceStat{{InstanceID: instance.ID, InflightRequests: inflight}}}, nil
	}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")

	started := time.Now()
	if err := engine.waitForRuntimeConfigInstanceDrain(ctx, app.ID, instance.ID, started.Add(-time.Second)); err != nil {
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
		stats      func(context.Context, string, string) (*StatsSnapshot, error)
		wantReason runtimeConfigDrainReason
	}{
		{
			name:       "instance omitted",
			stats:      func(context.Context, string, string) (*StatsSnapshot, error) { return &StatsSnapshot{}, nil },
			wantReason: runtimeConfigDrainReasonTelemetryMissing,
		},
		{
			name: "stats unavailable",
			stats: func(context.Context, string, string) (*StatsSnapshot, error) {
				return nil, errors.New("peer unavailable")
			},
			wantReason: runtimeConfigDrainReasonTelemetryMissing,
		},
		{
			name: "quiet period not elapsed",
			stats: func(_ context.Context, _, instanceID string) (*StatsSnapshot, error) {
				return &StatsSnapshot{Instances: []VMInstanceStat{{InstanceID: instanceID}}}, nil
			},
			wantReason: runtimeConfigDrainReasonQuietPeriod,
		},
		{
			name: "requests active",
			stats: func(_ context.Context, _, instanceID string) (*StatsSnapshot, error) {
				return &StatsSnapshot{Instances: []VMInstanceStat{{InstanceID: instanceID, InflightRequests: 1}}}, nil
			},
			wantReason: runtimeConfigDrainReasonRequestsActive,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			_, app, deployment := seedApp(t, store, api.PlanPro, 256, 5)
			instance, err := store.CreateInstance(ctx, app.ID, deployment.ID, string(state.StateDraining), app.RAMMB, "compute-old-owner", "wake-old")
			if err != nil {
				t.Fatalf("CreateInstance: %v", err)
			}
			vmm := &fakeVMM{}
			vmm.statsHook = func(callCtx context.Context, nodeID string) (*StatsSnapshot, error) {
				return tt.stats(callCtx, nodeID, instance.ID)
			}
			engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
			waitCtx, cancel := context.WithTimeout(ctx, 350*time.Millisecond)
			defer cancel()
			err = engine.waitForRuntimeConfigInstanceDrain(waitCtx, app.ID, instance.ID, time.Now().Add(-time.Second))
			if err == nil {
				t.Fatal("waitForRuntimeConfigInstanceDrain succeeded without a sustained, authoritative zero sample")
			}
			if !strings.Contains(err.Error(), "reason="+string(tt.wantReason)) {
				t.Fatalf("drain error %q does not expose reason %q", err, tt.wantReason)
			}
		})
	}
}
