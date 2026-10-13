package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMCPTasksStatusRequiresObserverHeartbeatForScaleToZero(t *testing.T) {
	cases := []struct {
		name      string
		heartbeat *mcpTasksMetricStatus
		wantOK    bool
	}{
		{name: "missing"},
		{name: "stale", heartbeat: &mcpTasksMetricStatus{Name: mcpTasksObserverHeartbeatMetric, Present: true, Value: 1, Stale: true}},
		{name: "unhealthy value", heartbeat: &mcpTasksMetricStatus{Name: mcpTasksObserverHeartbeatMetric, Present: true, Value: 0}},
		{name: "fresh", heartbeat: &mcpTasksMetricStatus{Name: mcpTasksObserverHeartbeatMetric, Present: true, Value: 1}, wantOK: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			metrics := []mcpTasksMetricStatus{
				{Name: mcpTasksOutstandingMetric, Present: true},
				{Name: mcpTasksOldestAgeMetric, Present: true},
				{Name: mcpTasksWithoutCompatibleWorkerMetric, Present: true},
			}
			if tc.heartbeat != nil {
				metrics = append(metrics, *tc.heartbeat)
			}
			result := mcpTasksStatusResult{
				ChecksEnabled:         true,
				ScaleToZeroConfigured: true,
				ObserverApp: &mcpTasksObserverReadiness{
					AppSlug: "mcp-observer", Linked: true, Ready: true, Status: "active",
					DeploymentAvailability: api.AppDeploymentAvailabilityLive,
					MinReplicas:            1, Replicas: 1, Violations: []string{},
				},
				Metrics:    metrics,
				Violations: []string{},
			}
			applyMCPTasksStatusChecks(&result, 0, -1, true)
			if gotOK := len(result.Violations) == 0; gotOK != tc.wantOK {
				t.Fatalf("violations = %v, want healthy=%t", result.Violations, tc.wantOK)
			}
		})
	}
}

func TestMCPTasksStatusRequiresReadyLinkedObserverForScaleToZero(t *testing.T) {
	result := mcpTasksStatusResult{
		ChecksEnabled:         true,
		ScaleToZeroConfigured: true,
		ObserverApp: &mcpTasksObserverReadiness{
			AppSlug: "mcp-observer", Linked: true, Ready: false,
			Violations: []string{"observer_app_has_no_running_replicas"},
		},
		Metrics: []mcpTasksMetricStatus{
			{Name: mcpTasksOutstandingMetric, Present: true},
			{Name: mcpTasksOldestAgeMetric, Present: true},
			{Name: mcpTasksWithoutCompatibleWorkerMetric, Present: true},
			{Name: mcpTasksObserverHeartbeatMetric, Present: true, Value: 1},
		},
		Violations: []string{},
	}
	applyMCPTasksStatusChecks(&result, 0, -1, true)
	if result.Health != "degraded" || len(result.Violations) == 0 {
		t.Fatalf("health=%q violations=%v, want observer readiness failure", result.Health, result.Violations)
	}
}

func TestMCPTasksStatusDoesNotRequireObserverHeartbeatWithWorkersKeptAlive(t *testing.T) {
	result := mcpTasksStatusResult{
		ChecksEnabled: true,
		Metrics: []mcpTasksMetricStatus{
			{Name: mcpTasksOutstandingMetric, Present: true},
			{Name: mcpTasksOldestAgeMetric, Present: true},
			{Name: mcpTasksWithoutCompatibleWorkerMetric, Present: true},
		},
		Violations: []string{},
	}
	applyMCPTasksStatusChecks(&result, 0, -1, true)
	if len(result.Violations) != 0 {
		t.Fatalf("violations = %v, want no violation", result.Violations)
	}
}

func TestMCPTasksStatusCanLimitQueuedTasksWithoutCompatibleWorkers(t *testing.T) {
	result := mcpTasksStatusResult{
		ChecksEnabled: true,
		Metrics: []mcpTasksMetricStatus{
			{Name: mcpTasksOutstandingMetric, Present: true},
			{Name: mcpTasksOldestAgeMetric, Present: true},
			{Name: mcpTasksWithoutCompatibleWorkerMetric, Present: true, Value: 2},
		},
		Violations: []string{},
	}
	applyMCPTasksStatusChecks(&result, 0, 1, false)
	if result.Health != "degraded" || len(result.Violations) != 1 {
		t.Fatalf("health=%q violations=%v, want compatibility backlog violation", result.Health, result.Violations)
	}
}

func TestMCPTasksObserverHeartbeatIsFreshOnlyForPublishedValueOne(t *testing.T) {
	cases := []struct {
		name   string
		metric mcpTasksMetricStatus
		want   bool
	}{
		{name: "missing", metric: mcpTasksMetricStatus{Name: mcpTasksObserverHeartbeatMetric}},
		{name: "stale", metric: mcpTasksMetricStatus{Name: mcpTasksObserverHeartbeatMetric, Present: true, Value: 1, Stale: true}},
		{name: "unhealthy value", metric: mcpTasksMetricStatus{Name: mcpTasksObserverHeartbeatMetric, Present: true, Value: 0}},
		{name: "fresh", metric: mcpTasksMetricStatus{Name: mcpTasksObserverHeartbeatMetric, Present: true, Value: 1}, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mcpTasksObserverHeartbeatIsFresh(tc.metric); got != tc.want {
				t.Fatalf("fresh = %t, want %t", got, tc.want)
			}
		})
	}
}
