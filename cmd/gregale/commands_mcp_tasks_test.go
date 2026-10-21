package main_test

import (
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestMCPTasksStatusRequiresObserverHeartbeatForScaleToZero(t *testing.T) {
	cases := []struct {
		name      string
		heartbeat *main.McpTasksMetricStatus
		wantOK    bool
	}{
		{name: "missing"},
		{name: "stale", heartbeat: &main.McpTasksMetricStatus{Name: main.McpTasksObserverHeartbeatMetric, Present: true, Value: 1, Stale: true}},
		{name: "unhealthy value", heartbeat: &main.McpTasksMetricStatus{Name: main.McpTasksObserverHeartbeatMetric, Present: true, Value: 0}},
		{name: "fresh", heartbeat: &main.McpTasksMetricStatus{Name: main.McpTasksObserverHeartbeatMetric, Present: true, Value: 1}, wantOK: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			metrics := []main.McpTasksMetricStatus{
				{Name: main.McpTasksOutstandingMetric, Present: true},
				{Name: main.McpTasksOldestAgeMetric, Present: true},
				{Name: main.McpTasksWithoutCompatibleWorkerMetric, Present: true},
			}
			if tc.heartbeat != nil {
				metrics = append(metrics, *tc.heartbeat)
			}
			result := main.McpTasksStatusResult{
				ChecksEnabled:         true,
				ScaleToZeroConfigured: true,
				ObserverApp: &main.McpTasksObserverReadiness{
					AppSlug: "mcp-observer", Linked: true, Ready: true, Status: "active",
					DeploymentAvailability: api.AppDeploymentAvailabilityLive,
					MinReplicas:            1, Replicas: 1, Violations: []string{},
				},
				Metrics:    metrics,
				Violations: []string{},
			}
			main.ApplyMCPTasksStatusChecks(&result, 0, -1, true)
			if gotOK := len(result.Violations) == 0; gotOK != tc.wantOK {
				t.Fatalf("violations = %v, want healthy=%t", result.Violations, tc.wantOK)
			}
		})
	}
}

func TestMCPTasksStatusRequiresReadyLinkedObserverForScaleToZero(t *testing.T) {
	result := main.McpTasksStatusResult{
		ChecksEnabled:         true,
		ScaleToZeroConfigured: true,
		ObserverApp: &main.McpTasksObserverReadiness{
			AppSlug: "mcp-observer", Linked: true, Ready: false,
			Violations: []string{"observer_app_has_no_running_replicas"},
		},
		Metrics: []main.McpTasksMetricStatus{
			{Name: main.McpTasksOutstandingMetric, Present: true},
			{Name: main.McpTasksOldestAgeMetric, Present: true},
			{Name: main.McpTasksWithoutCompatibleWorkerMetric, Present: true},
			{Name: main.McpTasksObserverHeartbeatMetric, Present: true, Value: 1},
		},
		Violations: []string{},
	}
	main.ApplyMCPTasksStatusChecks(&result, 0, -1, true)
	if result.Health != "degraded" || len(result.Violations) == 0 {
		t.Fatalf("health=%q violations=%v, want observer readiness failure", result.Health, result.Violations)
	}
}

func TestMCPTasksStatusDoesNotRequireObserverHeartbeatWithWorkersKeptAlive(t *testing.T) {
	result := main.McpTasksStatusResult{
		ChecksEnabled: true,
		Metrics: []main.McpTasksMetricStatus{
			{Name: main.McpTasksOutstandingMetric, Present: true},
			{Name: main.McpTasksOldestAgeMetric, Present: true},
			{Name: main.McpTasksWithoutCompatibleWorkerMetric, Present: true},
		},
		Violations: []string{},
	}
	main.ApplyMCPTasksStatusChecks(&result, 0, -1, true)
	if len(result.Violations) != 0 {
		t.Fatalf("violations = %v, want no violation", result.Violations)
	}
}

func TestMCPTasksStatusCanLimitQueuedTasksWithoutCompatibleWorkers(t *testing.T) {
	result := main.McpTasksStatusResult{
		ChecksEnabled: true,
		Metrics: []main.McpTasksMetricStatus{
			{Name: main.McpTasksOutstandingMetric, Present: true},
			{Name: main.McpTasksOldestAgeMetric, Present: true},
			{Name: main.McpTasksWithoutCompatibleWorkerMetric, Present: true, Value: 2},
		},
		Violations: []string{},
	}
	main.ApplyMCPTasksStatusChecks(&result, 0, 1, false)
	if result.Health != "degraded" || len(result.Violations) != 1 {
		t.Fatalf("health=%q violations=%v, want compatibility backlog violation", result.Health, result.Violations)
	}
}

func TestMCPTasksObserverHeartbeatIsFreshOnlyForPublishedValueOne(t *testing.T) {
	cases := []struct {
		name   string
		metric main.McpTasksMetricStatus
		want   bool
	}{
		{name: "missing", metric: main.McpTasksMetricStatus{Name: main.McpTasksObserverHeartbeatMetric}},
		{name: "stale", metric: main.McpTasksMetricStatus{Name: main.McpTasksObserverHeartbeatMetric, Present: true, Value: 1, Stale: true}},
		{name: "unhealthy value", metric: main.McpTasksMetricStatus{Name: main.McpTasksObserverHeartbeatMetric, Present: true, Value: 0}},
		{name: "fresh", metric: main.McpTasksMetricStatus{Name: main.McpTasksObserverHeartbeatMetric, Present: true, Value: 1}, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := main.McpTasksObserverHeartbeatIsFresh(tc.metric); got != tc.want {
				t.Fatalf("fresh = %t, want %t", got, tc.want)
			}
		})
	}
}
