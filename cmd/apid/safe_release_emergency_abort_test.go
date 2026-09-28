package main

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

type leaseHealthStub struct {
	health state.SafeReleaseWorkerLeaseHealth
	err    error
}

func (s *leaseHealthStub) SafeReleaseWorkerLeaseHealth(context.Context) (state.SafeReleaseWorkerLeaseHealth, error) {
	return s.health, s.err
}

func TestObserveSafeReleaseLeaseDistinguishesExpiryAndReadFailure(t *testing.T) {
	metrics := newSafeReleaseLeaseMetrics()
	now := time.Now().UTC()
	store := &leaseHealthStub{health: state.SafeReleaseWorkerLeaseHealth{CheckedAt: now, ExpiresAt: now.Add(time.Minute), Exists: true}}
	if err := observeSafeReleaseLease(t.Context(), store, metrics); err != nil {
		t.Fatal(err)
	}
	if testutil.ToFloat64(metrics.ready) != 1 || testutil.ToFloat64(metrics.checkSuccess) != 1 ||
		testutil.ToFloat64(metrics.secondsLeft) <= 0 ||
		math.Abs(testutil.ToFloat64(metrics.lastCheck)-float64(now.Unix())) > 2 {
		t.Fatal("fresh lease metrics not recorded")
	}
	store.health.ExpiresAt = now.Add(-time.Minute)
	if err := observeSafeReleaseLease(t.Context(), store, metrics); err != nil {
		t.Fatal(err)
	}
	if testutil.ToFloat64(metrics.ready) != 0 || testutil.ToFloat64(metrics.checkSuccess) != 1 || testutil.ToFloat64(metrics.secondsLeft) >= 0 {
		t.Fatal("expired lease metrics not recorded")
	}
	store.err = errors.New("database unavailable")
	if err := observeSafeReleaseLease(t.Context(), store, metrics); err == nil {
		t.Fatal("lease read error hidden")
	}
	if testutil.ToFloat64(metrics.ready) != 0 || testutil.ToFloat64(metrics.checkSuccess) != 0 {
		t.Fatal("lease read error looks healthy")
	}
}

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
	serving, err := emergencyAbortSweep(t.Context(), store, slog.Default(), func(s string) { outcome = s })
	if err != nil || serving != 1 || len(store.calls) != 1 || store.calls[0] != "app/active" || store.graces[0] != safeReleaseEmergencyGrace || outcome != "aborted" {
		t.Fatalf("sweep: serving=%d err=%v calls=%v graces=%v outcome=%q", serving, err, store.calls, store.graces, outcome)
	}
}

func TestEmergencyAbortSweepIncludesServingPendingCanary(t *testing.T) {
	pending := state.Deployment{ID: "pending", AppID: "app", Status: state.DeployLive, RolloutState: "pending", CanaryTotalSteps: 4, TrafficPercent: 1}
	store := &emergencyAbortStore{rows: []state.Deployment{pending}}
	if _, err := emergencyAbortSweep(t.Context(), store, slog.Default(), func(string) {}); err != nil {
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
	if _, err := emergencyAbortSweep(t.Context(), store, slog.Default(), func(string) {}); err != nil {
		t.Fatal(err)
	}
	if len(store.calls) != 1 {
		t.Fatalf("healthy lease calls = %v, want one check then stop", store.calls)
	}
}

func TestEmergencyAbortSweepReportsMissingLease(t *testing.T) {
	active := state.Deployment{ID: "active", AppID: "app", Status: state.DeployLive, RolloutState: "rolling_out", CanaryTotalSteps: 4, CanaryStep: 1, TrafficPercent: 10}
	store := &emergencyAbortStore{rows: []state.Deployment{active}, err: state.ErrSafeReleaseLeaseMissing}
	if _, err := emergencyAbortSweep(t.Context(), store, slog.Default(), func(string) {}); !errors.Is(err, state.ErrSafeReleaseLeaseMissing) {
		t.Fatalf("missing lease = %v", err)
	}
}
