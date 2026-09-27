package main

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

type emergencyAbortStore struct {
	rows   []state.Deployment
	calls  []string
	graces []time.Duration
	err    error
}

func (s *emergencyAbortStore) ListCanaryInFlight(context.Context) ([]state.Deployment, error) {
	return s.rows, nil
}

func (s *emergencyAbortStore) AbortCanaryOnExpiredWorkerLease(_ context.Context, appID, deploymentID string, grace time.Duration) (state.Deployment, int64, error) {
	s.calls = append(s.calls, appID+"/"+deploymentID)
	s.graces = append(s.graces, grace)
	if s.err != nil {
		return state.Deployment{}, 0, s.err
	}
	return state.Deployment{ID: deploymentID, AppID: appID, TrafficPercent: 0}, 42, nil
}

func TestEmergencyAbortSweepOnlyTouchesServingCanaries(t *testing.T) {
	active := state.Deployment{ID: "active", AppID: "app", Status: state.DeployLive, RolloutState: "rolling_out", CanaryTotalSteps: 4, CanaryStep: 1, TrafficPercent: 10}
	zeroTraffic := active
	zeroTraffic.ID, zeroTraffic.TrafficPercent = "zero", 0
	complete := active
	complete.ID, complete.CanaryStep = "complete", 4
	store := &emergencyAbortStore{rows: []state.Deployment{zeroTraffic, complete, active}}
	var outcome string
	err := emergencyAbortSweep(t.Context(), store, slog.Default(), func(s string) { outcome = s })
	if err != nil || len(store.calls) != 1 || store.calls[0] != "app/active" || store.graces[0] != safeReleaseEmergencyGrace || outcome != "aborted" {
		t.Fatalf("sweep: err=%v calls=%v graces=%v outcome=%q", err, store.calls, store.graces, outcome)
	}
}

func TestEmergencyAbortSweepIncludesServingPendingCanary(t *testing.T) {
	pending := state.Deployment{ID: "pending", AppID: "app", Status: state.DeployLive, RolloutState: "pending", CanaryTotalSteps: 4, TrafficPercent: 1}
	store := &emergencyAbortStore{rows: []state.Deployment{pending}}
	if err := emergencyAbortSweep(t.Context(), store, slog.Default(), func(string) {}); err != nil {
		t.Fatal(err)
	}
	if len(store.calls) != 1 || store.calls[0] != "app/pending" {
		t.Fatalf("pending canary calls = %v", store.calls)
	}
}

func TestEmergencyAbortSweepStopsWhenLeaseIsHealthy(t *testing.T) {
	active := state.Deployment{ID: "active", AppID: "app", Status: state.DeployLive, RolloutState: "rolling_out", CanaryTotalSteps: 4, CanaryStep: 1, TrafficPercent: 10}
	second := active
	second.ID = "second"
	store := &emergencyAbortStore{rows: []state.Deployment{active, second}, err: state.ErrSafeReleaseLeaseNotExpired}
	if err := emergencyAbortSweep(t.Context(), store, slog.Default(), func(string) {}); err != nil {
		t.Fatal(err)
	}
	if len(store.calls) != 1 {
		t.Fatalf("healthy lease calls = %v, want one check then stop", store.calls)
	}
}

func TestEmergencyAbortSweepReportsMissingLease(t *testing.T) {
	active := state.Deployment{ID: "active", AppID: "app", Status: state.DeployLive, RolloutState: "rolling_out", CanaryTotalSteps: 4, CanaryStep: 1, TrafficPercent: 10}
	store := &emergencyAbortStore{rows: []state.Deployment{active}, err: state.ErrSafeReleaseLeaseMissing}
	if err := emergencyAbortSweep(t.Context(), store, slog.Default(), func(string) {}); !errors.Is(err, state.ErrSafeReleaseLeaseMissing) {
		t.Fatalf("missing lease = %v", err)
	}
}
