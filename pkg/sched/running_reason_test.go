// spec: §6.1

package sched

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestExplainRunningReportsSchedulerBlockers(t *testing.T) {
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	stale := now.Add(-10 * time.Minute)
	tests := []struct {
		name  string
		in    []InstanceInfo
		want  string
		check func(t *testing.T, got runningReasonObservation)
	}{
		{
			name: "recent request extends idle deadline",
			in:   []InstanceInfo{{AppID: "app", Plan: api.PlanPro, State: state.StateRunning, LastRequest: now.Add(-5 * time.Second), Started: stale, IdleTimeoutS: 60}},
			want: api.DebugRunningReasonRequestActivity,
		},
		{
			name: "open connection",
			in:   []InstanceInfo{{AppID: "app", Plan: api.PlanPro, State: state.StateRunning, LastRequest: stale, Started: stale, IdleTimeoutS: 60, OpenConns: 3}},
			want: api.DebugRunningReasonOpenConnection,
			check: func(t *testing.T, got runningReasonObservation) {
				if got.Causes[0].OpenConnections != 3 {
					t.Fatalf("open connections = %d, want 3", got.Causes[0].OpenConnections)
				}
			},
		},
		{
			name: "tail tasks",
			in:   []InstanceInfo{{AppID: "app", Plan: api.PlanPro, State: state.StateRunning, LastRequest: stale, Started: stale, IdleTimeoutS: 60, TailCount: 2}},
			want: api.DebugRunningReasonTailTasks,
		},
		{
			name: "minimum floor holds stale instance",
			in: []InstanceInfo{
				{AppID: "app", Plan: api.PlanPro, State: state.StateRunning, LastRequest: stale, Started: stale, IdleTimeoutS: 60, MinInstances: 1, ConfiguredMinInstances: 1},
				{AppID: "app", Plan: api.PlanPro, State: state.StateRunning, LastRequest: stale, Started: stale, IdleTimeoutS: 60, MinInstances: 1, ConfiguredMinInstances: 1},
			},
			want: api.DebugRunningReasonMinInstances,
		},
		{
			name: "scale-in cooldown",
			in:   []InstanceInfo{{AppID: "app", Plan: api.PlanPro, State: state.StateRunning, LastRequest: stale, Started: stale, IdleTimeoutS: 60, LastScaleInAt: runningReasonPtrTime(now.Add(-5 * time.Second)), ScaleInCooldownS: 30}},
			want: api.DebugRunningReasonScaleInCooldown,
		},
		{
			name: "worker workload mode",
			in:   []InstanceInfo{{AppID: "app", Plan: api.PlanPro, State: state.StateRunning, WorkloadClass: state.WorkloadClassWorker, Mode: string(state.InstanceModeWorker), LastRequest: stale, Started: stale, IdleTimeoutS: 60}},
			want: api.DebugRunningReasonWorkloadMode,
		},
		{
			name: "missing activity is conservative",
			in:   []InstanceInfo{{AppID: "app", Plan: api.PlanPro, State: state.StateRunning, IdleTimeoutS: 60}},
			want: api.DebugRunningReasonUnknownActivity,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := explainRunning(now, tt.in)["app"]
			if len(got.Causes) == 0 || got.Causes[0].Code != tt.want {
				codes := make([]string, 0, len(got.Causes))
				for _, cause := range got.Causes {
					codes = append(codes, cause.Code)
				}
				t.Fatalf("causes = %v, want first cause %q", codes, tt.want)
			}
			if tt.check != nil {
				tt.check(t, got)
			}
		})
	}
}

func TestExplainRunningMarksDegradedFlowObservation(t *testing.T) {
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	got := explainRunning(now, []InstanceInfo{{
		AppID:             "app",
		Plan:              api.PlanPro,
		State:             state.StateRunning,
		LastRequest:       now.Add(-10 * time.Minute),
		Started:           now.Add(-10 * time.Minute),
		IdleTimeoutS:      60,
		FlowCountDegraded: true,
	}})["app"]
	if !got.Degraded {
		t.Fatal("Degraded = false, want true when flow count was unavailable")
	}
	if got.Causes[0].Code != api.DebugRunningReasonNoBlockerObserved {
		t.Fatalf("causes = %+v, want no_blocker_observed", got.Causes)
	}
}

func TestRunningEventFingerprintIgnoresObservationTime(t *testing.T) {
	event := runningReasonEvent{SchemaVersion: debugRunningSchemaVersion, AppID: "app", ObservedAt: "2026-09-12T12:00:00Z", RunningInstances: 1, Causes: []api.DebugRunningCause{{Code: api.DebugRunningReasonOpenConnection, Summary: "open", InstanceCount: 1}}}
	first := runningEventFingerprint(event)
	event.ObservedAt = "2026-09-12T12:00:10Z"
	if got := runningEventFingerprint(event); got != first {
		t.Fatalf("fingerprint changed with observation time: %q != %q", got, first)
	}
}

func TestRecordRunningReasonObservationsDeduplicatesAndThrottles(t *testing.T) {
	store := state.NewMemStore()
	loop := &Loop{engine: &Engine{store: store}}
	appID := uuid.NewString()
	accountID := uuid.NewString()
	apps := []state.App{{ID: appID, AccountID: accountID}}
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	snapshot := []InstanceInfo{{AppID: appID, Plan: api.PlanPro, State: state.StateRunning, LastRequest: now.Add(-10 * time.Second), Started: now.Add(-time.Minute), IdleTimeoutS: 60}}

	loop.recordRunningReasonObservations(t.Context(), apps, snapshot, now)
	loop.recordRunningReasonObservations(t.Context(), apps, snapshot, now.Add(5*time.Second))
	events, err := store.ListEvents(t.Context(), appID, 10)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events after unchanged observation = %d, want 1", len(events))
	}

	changed := snapshot
	changed[0].OpenConns = 1
	loop.recordRunningReasonObservations(t.Context(), apps, changed, now.Add(10*time.Second))
	events, _ = store.ListEvents(t.Context(), appID, 10)
	if len(events) != 1 {
		t.Fatalf("events during throttle interval = %d, want 1", len(events))
	}
	loop.recordRunningReasonObservations(t.Context(), apps, changed, now.Add(16*time.Second))
	events, _ = store.ListEvents(t.Context(), appID, 10)
	if len(events) != 2 {
		t.Fatalf("events after throttle interval = %d, want 2", len(events))
	}
}

func runningReasonPtrTime(value time.Time) *time.Time { return &value }
