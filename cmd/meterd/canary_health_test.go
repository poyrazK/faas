package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/canary"
	"github.com/onebox-faas/faas/pkg/state"
)

type canaryHealthSummary struct {
	requests             int64
	serverErrors         int64
	p95LatencyMS         float64
	coldBootRequests     int64
	coldBootP95LatencyMS float64
	cpuUsec              int64
	cpuRequests          int64
}

type canaryHealthTestStore struct {
	state.Store
	live      []state.Deployment
	summaries map[string]canaryHealthSummary
}

func (s *canaryHealthTestStore) LiveDeployments(_ context.Context, _ string) ([]state.Deployment, error) {
	return s.live, nil
}

func (s *canaryHealthTestStore) RequestTelemetryCircuitBreakerSummary(_ context.Context, _, deploymentID string, _, _ time.Time) (int64, int64, float64, int64, float64, int64, int64, error) {
	if summary, ok := s.summaries[deploymentID]; ok {
		return summary.requests, summary.serverErrors, summary.p95LatencyMS, summary.coldBootRequests, summary.coldBootP95LatencyMS, summary.cpuUsec, summary.cpuRequests, nil
	}
	return 0, 0, 0, 0, 0, 0, 0, nil
}

type canaryHealthPromQL struct {
	values  []float64
	queries []string
	err     error
}

func (p *canaryHealthPromQL) QueryScalar(_ context.Context, query string) (float64, error) {
	p.queries = append(p.queries, query)
	if p.err != nil {
		return 0, p.err
	}
	if len(p.values) == 0 {
		return 0, fmt.Errorf("unexpected PromQL query %q", query)
	}
	value := p.values[0]
	p.values = p.values[1:]
	return value, nil
}

func TestCanaryCircuitBreakerObservationRequiresOOMMetricCoverage(t *testing.T) {
	now := time.Now().UTC()
	appID, candidateID, stableID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	store := &canaryHealthTestStore{
		live: []state.Deployment{
			{ID: candidateID, AppID: appID, Scope: "production", TrafficPercent: 10},
			{ID: stableID, AppID: appID, Scope: "production", TrafficPercent: 90},
		},
		summaries: map[string]canaryHealthSummary{
			candidateID: {requests: 30, coldBootRequests: 6, p95LatencyMS: 140, coldBootP95LatencyMS: 700, cpuUsec: 60_000_000, cpuRequests: 30},
			stableID:    {requests: 80, coldBootRequests: 10, p95LatencyMS: 100, coldBootP95LatencyMS: 500, cpuUsec: 160_000_000, cpuRequests: 80},
		},
	}
	row := canary.CanaryRow{
		ID:             candidateID,
		AppID:          appID,
		Scope:          "production",
		RolloutStarted: now.Add(-time.Minute),
	}
	t.Run("missing metric family is unavailable, not healthy zero", func(t *testing.T) {
		prom := &canaryHealthPromQL{values: []float64{0}}
		got, err := (&canaryStoreAdapter{store: store, promQL: prom}).CircuitBreakerObservation(context.Background(), row, now.Add(-2*time.Minute), now)
		if err != nil {
			t.Fatalf("CircuitBreakerObservation: %v", err)
		}
		if !got.HasStable || got.StableDeploymentID != stableID {
			t.Fatalf("stable predecessor = %+v, want %s", got, stableID)
		}
		if !got.CPURequestSignalAvailable || got.Candidate.CPURequests != 30 || got.Candidate.CPUUsec != 60_000_000 {
			t.Fatalf("CPU/request observation = available:%v candidate:%+v, want populated candidate summary", got.CPURequestSignalAvailable, got.Candidate)
		}
		if got.OOMSignalAvailable {
			t.Fatal("OOM signal marked available without an exported metric family")
		}
		if len(prom.queries) != 1 || !strings.Contains(prom.queries[0], "count(") {
			t.Fatalf("queries = %q, want only metric-coverage query", prom.queries)
		}
		if got.Candidate.ColdBootRequests != 6 || got.Candidate.ColdBootP95LatencyMS != 700 {
			t.Fatalf("candidate health = %+v, cold-boot telemetry was not propagated", got.Candidate)
		}
	})

	t.Run("coverage permits exact deployment OOM query", func(t *testing.T) {
		prom := &canaryHealthPromQL{values: []float64{1, 0, 1, 30, 6, 80, 1}}
		got, err := (&canaryStoreAdapter{store: store, promQL: prom}).CircuitBreakerObservation(context.Background(), row, now.Add(-2*time.Minute), now)
		if err != nil {
			t.Fatalf("CircuitBreakerObservation: %v", err)
		}
		if !got.OOMSignalAvailable || got.OOMKills != 0 {
			t.Fatalf("OOM observation = available:%v kills:%g, want available/zero", got.OOMSignalAvailable, got.OOMKills)
		}
		if !got.DependencySignalAvailable || got.Candidate.DependencyCalls != 30 || got.Candidate.DependencyErrors != 6 ||
			got.Stable.DependencyCalls != 80 || got.Stable.DependencyErrors != 1 {
			t.Fatalf("dependency observation = available:%v candidate:%+v stable:%+v", got.DependencySignalAvailable, got.Candidate, got.Stable)
		}
		if len(prom.queries) != 7 || !strings.Contains(prom.queries[1], candidateID) ||
			!strings.Contains(prom.queries[4], candidateID) || !strings.Contains(prom.queries[6], stableID) {
			t.Fatalf("queries = %q, want OOM coverage/candidate plus dependency coverage and candidate/stable counters", prom.queries)
		}
	})

	t.Run("missing dependency metric family is unavailable", func(t *testing.T) {
		prom := &canaryHealthPromQL{values: []float64{1, 0, 0}}
		got, err := (&canaryStoreAdapter{store: store, promQL: prom}).CircuitBreakerObservation(context.Background(), row, now.Add(-2*time.Minute), now)
		if err != nil {
			t.Fatalf("CircuitBreakerObservation: %v", err)
		}
		if !got.OOMSignalAvailable || got.DependencySignalAvailable {
			t.Fatalf("signal availability = OOM:%v dependency:%v, want OOM available and dependency unavailable", got.OOMSignalAvailable, got.DependencySignalAvailable)
		}
		if len(prom.queries) != 3 {
			t.Fatalf("queries = %q, want no per-deployment queries when family is missing", prom.queries)
		}
	})

	t.Run("Prometheus failure is surfaced", func(t *testing.T) {
		prom := &canaryHealthPromQL{err: errors.New("prometheus unavailable")}
		if _, err := (&canaryStoreAdapter{store: store, promQL: prom}).CircuitBreakerObservation(context.Background(), row, now.Add(-2*time.Minute), now); err == nil || !strings.Contains(err.Error(), "metric coverage") {
			t.Fatalf("CircuitBreakerObservation error = %v, want metric-coverage error", err)
		}
	})
}
