package main

import (
	"context"
	"math"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var mcpOverviewTaskMetrics = []string{
	mcpTasksOutstandingMetric, mcpTasksOldestAgeMetric, mcpTasksWithoutCompatibleWorkerMetric,
	"mcp_tasks_running", "mcp_tasks_failed", "mcp_tasks_capacity_waiting", "mcp_tasks_retry_waiting",
	"mcp_tasks_active_workers", "mcp_tasks_draining_workers", "mcp_tasks_unsupported_handler_tasks", mcpTasksObserverHeartbeatMetric,
}

func collectMCPOverviewTasks(ctx context.Context, client *Client, options mcpOverviewOptions) mcpOverviewCollection {
	result := mcpOverviewCollection{Tasks: mcpOverviewTasks{State: "not_requested"}, ReleasesComplete: true}
	if options.Worker == "" {
		return result
	}
	result.Tasks = mcpOverviewTasks{State: "unknown", App: options.Worker}
	result.ReleasesComplete = false
	inspect := mcpOverviewInspection("Inspect Task metrics", "mcp", "tasks", "status", "--app", options.Worker, "--require-fresh", "--details")
	worker, err := client.GetApp(ctx, options.Worker)
	if err != nil || worker.Slug != options.Worker {
		result.Issues = append(result.Issues, mcpOverviewIssue{"tasks", "worker_unavailable", "unknown", "Could not inspect the worker application. Check read permissions, application selection and connectivity.", inspect})
		return result
	}
	if !isWorkerApp(worker) {
		result.Tasks.State = "degraded"
		result.Issues = append(result.Issues, mcpOverviewIssue{"tasks", "invalid_worker_mode", "error", "The selected Task application is not configured for worker execution.", inspect})
		return result
	}
	deployments, err := client.ListAppDeploymentsAll(ctx, options.Worker)
	result.Holds = mcpOverviewHolds(options.Worker, deployments, time.Now())
	result.ReleasesComplete = err == nil
	if err != nil {
		result.Issues = append(result.Issues, mcpOverviewIssue{"releases", "worker_deployments_unavailable", "unknown", "Worker deployment history could not be fully inspected; additional promotion holds may exist.", inspect})
	}
	instances, instanceErr := client.ListInstancesWithHistory(ctx, options.Worker, false)
	if instanceErr == nil {
		count := 0
		for _, instance := range instances {
			if humanizeInstanceState(instance.State) == "running" {
				count++
			}
		}
		result.Tasks.RunningReplicas = &count
	} else {
		result.Issues = append(result.Issues, mcpOverviewIssue{"tasks", "worker_replicas_unavailable", "unknown", "Running worker replicas could not be inspected.", inspect})
	}
	metrics, metricErr := client.GetAppCustomMetrics(ctx, options.Worker)
	if metricErr != nil {
		result.Issues = append(result.Issues, mcpOverviewIssue{"tasks", "task_metrics_unavailable", "unknown", "Task metrics could not be read. Queue counts remain unknown.", inspect})
		summary := buildMCPOverviewTaskSummary(worker, api.CustomMetricListResponse{})
		result.Tasks.Summary = &summary
		collectMCPOverviewObserver(ctx, client, worker, options, &result)
		summary.Health = result.Tasks.State
		return result
	}
	summary := buildMCPOverviewTaskSummary(worker, metrics)
	result.Tasks.Summary = &summary
	result.Tasks.State = "healthy"
	if !summary.Configured {
		result.Tasks.State = "degraded"
		result.Issues = append(result.Issues, mcpOverviewIssue{"tasks", "task_scaling_unconfigured", "error", "Task backlog scaling is not configured consistently.", mcpOverviewInspection("Preview Task scaling", "mcp", "tasks", "setup", "--app", options.Worker)})
	}
	for i := 0; i < 3; i++ {
		metric := summary.Metrics[i]
		if !metric.Present || metric.Stale {
			if result.Tasks.State != "degraded" {
				result.Tasks.State = "unknown"
			}
			result.Issues = append(result.Issues, mcpOverviewIssue{"tasks", "required_metric_unavailable", "unknown", metric.Name + " is missing, stale or invalid; its value cannot establish queue health.", inspect})
		} else if metric.Name == mcpTasksWithoutCompatibleWorkerMetric && metric.Value > 0 {
			result.Tasks.State = "degraded"
			result.Issues = append(result.Issues, mcpOverviewIssue{"tasks", "incompatible_task_workers", "error", "Pending Tasks have no live compatible handler version. Retain or deploy compatible workers before releasing.", inspect})
		}
	}
	if instanceErr != nil && result.Tasks.State == "healthy" {
		result.Tasks.State = "unknown"
	}
	for _, diagnostic := range summary.Diagnostics {
		if diagnostic.Code == "telemetry_incomplete" || diagnostic.Code == "observer_health_unknown" {
			continue
		}
		result.Issues = append(result.Issues, mcpOverviewIssue{"tasks", diagnostic.Code, "info", diagnostic.Message, inspect})
	}
	details, detailErr := client.GetAppMCPTaskCompatibilityReport(ctx, options.Worker)
	if detailErr == nil {
		summary.HandlerCompatibility = &details
		result.Tasks.Summary = &summary
	}
	if detailErr != nil || !details.Published || details.Stale || details.Truncated {
		result.Issues = append(result.Issues, mcpOverviewIssue{"tasks", "handler_details_unavailable", "info", "Complete fresh handler-version details are unavailable; use aggregate compatibility metrics cautiously and inspect the observer before a release.", inspect})
	} else {
		for _, handler := range details.Handlers {
			if handler.WaitingTasks > 0 && handler.LiveCompatibleWorkers == 0 {
				result.Tasks.State = "degraded"
				result.Issues = append(result.Issues, mcpOverviewIssue{"tasks", "handler_version_without_workers", "error", "The fresh observer report includes a pending handler version without a live compatible worker.", inspect})
				break
			}
		}
	}
	collectMCPOverviewObserver(ctx, client, worker, options, &result)
	summary.Health = result.Tasks.State
	return result
}

func buildMCPOverviewTaskSummary(worker api.AppResponse, metrics api.CustomMetricListResponse) mcpTasksStatusResult {
	summary := mcpTasksStatusResult{AppSlug: worker.Slug, FreshnessSeconds: metrics.FreshnessS, Metrics: []mcpTasksMetricStatus{}, Violations: []string{}}
	if worker.Manifest.WorkerReplicas != nil {
		summary.Scaling = *worker.Manifest.WorkerReplicas
		summary.Configured = summary.Scaling.Metric == api.ScalingMetricCustom && summary.Scaling.Name == mcpTasksOutstandingMetric && summary.Scaling.Target > 0 && mcpTasksPolicyMatches(worker.ScalingPolicy, summary.Scaling)
		summary.ScaleToZeroConfigured = summary.Configured && summary.Scaling.Min == 0
	}
	for _, name := range mcpOverviewTaskMetrics {
		entry := mcpTasksMetricStatus{Name: name}
		for _, metric := range metrics.Metrics {
			if metric.Name == name {
				observed := metric.ObservedAt
				entry.Present = true
				entry.Value = metric.Value
				entry.ObservedAt = &observed
				entry.Stale = metric.Stale || observed.IsZero() || math.IsNaN(metric.Value) || math.IsInf(metric.Value, 0) || metric.Value < 0
				break
			}
		}
		summary.Metrics = append(summary.Metrics, entry)
	}
	summary.OutstandingMetricFresh = summary.Metrics[0].Present && !summary.Metrics[0].Stale
	summary.diagnose()
	// Optional Node telemetry is not required from the compact Go/Python
	// publishers. Missing values stay explicit rather than becoming zeros.
	summary.Diagnostics = filterMCPOverviewTaskDiagnostics(summary.Diagnostics)
	return summary
}

func filterMCPOverviewTaskDiagnostics(diagnostics []mcpTasksDiagnostic) []mcpTasksDiagnostic {
	filtered := []mcpTasksDiagnostic{}
	for _, diagnostic := range diagnostics {
		if diagnostic.Code != "telemetry_incomplete" && diagnostic.Code != "observer_health_unknown" {
			filtered = append(filtered, diagnostic)
		}
	}
	return filtered
}

func collectMCPOverviewObserver(ctx context.Context, client *Client, worker api.AppResponse, options mcpOverviewOptions, result *mcpOverviewCollection) {
	summary := result.Tasks.Summary
	slug := options.Observer
	if slug == "" {
		slug = summary.Scaling.ObserverAppSlug
	}
	if slug == "" && !summary.ScaleToZeroConfigured {
		return
	}
	readiness := loadMCPTasksObserverReadiness(ctx, client, worker, slug)
	readiness.Linked = slug != "" && summary.Scaling.ObserverAppSlug == slug
	view := mcpOverviewObserver{Readiness: readiness, HeartbeatFresh: summary.ObserverHeartbeatFresh}
	if slug != "" {
		observer, err := client.GetApp(ctx, slug)
		if err == nil {
			view.ProcessDetected = mcpTasksObserverEntrypointDetected(observer.Manifest.Entrypoint) || mcpTasksObserverRoleConfigured(observer.Manifest.Env, observer.Manifest.Entrypoint)
			view.NamespaceMatches = mcpTasksObserverNamespacesMatch(worker, observer)
			view.MetricsTargetMatches = mcpTasksObserverMetricsTargetMatches(observer, worker.Slug)
		}
	}
	result.Tasks.Observer = &view
	summary.ObserverApp = &view.Readiness
	if !view.Readiness.Ready || !view.ProcessDetected || !view.NamespaceMatches || !view.MetricsTargetMatches || !view.HeartbeatFresh || (summary.ScaleToZeroConfigured && !readiness.Linked) {
		if result.Tasks.State != "degraded" {
			result.Tasks.State = "unknown"
		}
		action := mcpOverviewInspection("Inspect the observer", "mcp", "tasks", "observer", "status", "--worker-app", options.Worker)
		if slug != "" {
			action.Arguments = append(action.Arguments, "--app", slug)
		}
		result.Issues = append(result.Issues, mcpOverviewIssue{"observer", "observer_not_ready", "unknown", "Observer deployment, role, namespace, metric destination or fresh heartbeat is not verified. Do not enable scale-to-zero or release incompatible workers until it is ready.", action})
	}
}
