package main

import "testing"

func TestMCPTaskDiagnosticsUseOnlyFreshSignals(t *testing.T) {
	names := []string{mcpTasksOutstandingMetric, mcpTasksOldestAgeMetric, "mcp_tasks_running", "mcp_tasks_capacity_waiting", "mcp_tasks_retry_waiting", "mcp_tasks_failed", "mcp_tasks_active_workers", "mcp_tasks_unsupported_handler_tasks", "mcp_tasks_observer_heartbeat"}
	result := mcpTasksStatusResult{ScaleToZeroConfigured: true}
	for _, name := range names {
		result.Metrics = append(result.Metrics, mcpTasksMetricStatus{Name: name, Present: true})
	}
	result.Metrics[0].Value = 5
	result.Metrics[6].Value = 1
	result.Metrics[7].Value = 3
	result.Metrics[8].Value = 1
	result.diagnose()
	has := func(code string) bool {
		for _, diagnostic := range result.Diagnostics {
			if diagnostic.Code == code {
				return true
			}
		}
		return false
	}
	if !has("unsupported_handler") || !result.WorkerHeartbeatsFresh || !result.ObserverHeartbeatFresh {
		t.Fatal("fresh worker and handler signals were not reported")
	}
	result.Metrics[7].Stale = true
	result.Metrics[8].Stale = true
	result.diagnose()
	if has("unsupported_handler") || has("idle_queue") || !has("telemetry_incomplete") || !has("observer_health_unknown") || result.ObserverHeartbeatFresh {
		t.Fatal("stale metrics were interpreted as current health")
	}
	result.Metrics[6].Present = false
	result.diagnose()
	if has("no_active_workers") || result.WorkerHeartbeatsFresh {
		t.Fatal("missing worker telemetry was interpreted as no workers")
	}
}
