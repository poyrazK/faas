package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	mcpTasksOutstandingMetric = "mcp_tasks_outstanding"
	mcpTasksOldestAgeMetric   = "mcp_tasks_oldest_age_seconds"
)

func cmdMCPTasks(args []string) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		PrintUsage(os.Stderr,
			"usage: gregale mcp tasks <setup|status> --app <worker-app> [flags]", "mcp")
		return 0
	}
	switch args[0] {
	case "setup":
		return cmdMCPTasksSetup(args[1:])
	case "status":
		return cmdMCPTasksStatus(args[1:])
	default:
		return printErr("Unknown MCP tasks command", fmt.Errorf("%q", args[0]))
	}
}

type mcpTasksSetupResult struct {
	AppSlug     string             `json:"app_slug"`
	Applied     bool               `json:"applied"`
	Changed     bool               `json:"changed"`
	NeedsUpdate bool               `json:"needs_update"`
	Current     *api.WorkerScaling `json:"current,omitempty"`
	Proposed    api.WorkerScaling  `json:"proposed"`
}

func cmdMCPTasksSetup(args []string) int {
	fs := newFlagSet("mcp-tasks-setup", flag.ContinueOnError)
	appFlag := fs.String("app", "", "worker app slug (defaults to the linked project app)")
	minFlag := fs.Int("min", -1, "minimum worker replicas; default 1, use 0 only with a running task observer")
	maxFlag := fs.Int("max", -1, "maximum worker replicas; default 10")
	targetFlag := fs.Float64("target", -1, "outstanding tasks per worker; default 4")
	apply := fs.Bool("apply", false, "apply the proposed worker scaling policy")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if fs.NArg() != 0 || *minFlag < -1 || *maxFlag < -1 ||
		(*targetFlag < 0 && *targetFlag != -1) || math.IsNaN(*targetFlag) ||
		math.IsInf(*targetFlag, 0) {
		return printErr("Invalid MCP task setup flags", errors.New("unexpected positional arguments or invalid scaling values"))
	}

	slug, err := resolveAppFlagOrContext(*appFlag)
	if err != nil {
		return printErr("Could not resolve worker app", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}

	ctx := context.Background()
	app, err := client.GetApp(ctx, slug)
	if err != nil {
		return printErr("Could not get worker app", err)
	}
	if !isWorkerApp(app) {
		return printErr("Invalid app mode", fmt.Errorf("app %q is not configured for worker execution", slug))
	}

	proposed := api.WorkerScaling{
		Min: 1, Max: 10, Metric: api.ScalingMetricCustom,
		Name: mcpTasksOutstandingMetric, Target: 4,
	}
	if current := app.Manifest.WorkerReplicas; current != nil {
		proposed = *current
		proposed.Metric = api.ScalingMetricCustom
		proposed.Name = mcpTasksOutstandingMetric
		if proposed.Target <= 0 {
			proposed.Target = 4
		}
	}
	if *minFlag >= 0 {
		proposed.Min = *minFlag
	}
	if *maxFlag >= 0 {
		proposed.Max = *maxFlag
	}
	if *targetFlag >= 0 {
		proposed.Target = *targetFlag
	}
	if proposed.Min < 0 || proposed.Max < 1 || proposed.Min > proposed.Max ||
		proposed.Target <= 0 {
		return printErr("Invalid MCP task scaling policy", errors.New("replicas must satisfy 0 <= min <= max, max must be at least 1, and target must be greater than zero"))
	}
	if problem := api.ValidateCustomMetricName(proposed.Name); problem != nil {
		return printErr("Invalid MCP task metric", errors.New(problem.Detail))
	}

	current := app.Manifest.WorkerReplicas
	needsUpdate := current == nil || *current != proposed || !mcpTasksPolicyMatches(app.ScalingPolicy, proposed)
	result := mcpTasksSetupResult{AppSlug: app.Slug, Applied: *apply, NeedsUpdate: needsUpdate, Current: current, Proposed: proposed}
	if *apply && needsUpdate {
		policy := api.ScalingPolicy{}
		if app.ScalingPolicy != nil {
			policy = *app.ScalingPolicy
		}
		policy.MinInstances = proposed.Min
		policy.MaxInstances = proposed.Max
		policy.Targets = nil
		policy.Target = &api.ScalingTarget{Metric: proposed.Metric, Name: proposed.Name, Value: proposed.Target}
		updated, updateErr := client.UpdateApp(ctx, app.Slug, api.UpdateAppRequest{
			WorkerReplicas: &proposed,
			ScalingPolicy:  &policy,
		})
		if updateErr != nil {
			return printErr("Could not configure MCP task scaling", updateErr)
		}
		result.Changed = true
		result.NeedsUpdate = false
		if updated.Manifest.WorkerReplicas != nil {
			result.Proposed = *updated.Manifest.WorkerReplicas
		}
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	if result.Changed {
		PrintOK(osStdout, "MCP task scaling updated for %s", result.AppSlug)
	} else if result.NeedsUpdate {
		_, _ = fmt.Fprintf(osStdout, "MCP task scaling preview for %s (no changes applied)\n", result.AppSlug)
	} else {
		_, _ = fmt.Fprintf(osStdout, "MCP task scaling is already configured for %s\n", result.AppSlug)
	}
	_, _ = fmt.Fprintf(osStdout, "  Replicas: min=%d, max=%d\n", result.Proposed.Min, result.Proposed.Max)
	_, _ = fmt.Fprintf(osStdout, "  Metric:   %s (%s), target %.0f tasks per worker\n", result.Proposed.Metric, result.Proposed.Name, result.Proposed.Target)
	if result.NeedsUpdate {
		_, _ = fmt.Fprintln(osStdout, "  Re-run with --apply to save this policy.")
	}
	if result.Proposed.Min == 0 {
		_, _ = fmt.Fprintln(osStdout, "  Scale-to-zero requires a separate always-on task observer publishing this metric.")
	}
	return 0
}

func mcpTasksPolicyMatches(policy *api.ScalingPolicy, scaling api.WorkerScaling) bool {
	if policy == nil || policy.MinInstances != scaling.Min ||
		policy.MaxInstances != scaling.Max || len(policy.Targets) != 0 ||
		policy.Target == nil {
		return false
	}
	return policy.Target.Metric == scaling.Metric && policy.Target.Name == scaling.Name && policy.Target.Value == scaling.Target
}

type mcpTasksMetricStatus struct {
	Name       string     `json:"name"`
	Present    bool       `json:"present"`
	Value      float64    `json:"value"`
	ObservedAt *time.Time `json:"observed_at,omitempty"`
	Stale      bool       `json:"stale"`
}

type mcpTasksDiagnostic struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type mcpTasksStatusResult struct {
	WorkerHeartbeatsFresh  bool                 `json:"worker_heartbeats_fresh"`
	ObserverHeartbeatFresh bool                 `json:"observer_heartbeat_fresh"`
	Diagnostics            []mcpTasksDiagnostic `json:"diagnostics"`

	AppSlug                string                 `json:"app_slug"`
	Configured             bool                   `json:"configured"`
	ScaleToZeroConfigured  bool                   `json:"scale_to_zero_configured"`
	OutstandingMetricFresh bool                   `json:"outstanding_metric_fresh"`
	FreshnessSeconds       int                    `json:"freshness_seconds"`
	Scaling                api.WorkerScaling      `json:"scaling"`
	Metrics                []mcpTasksMetricStatus `json:"metrics"`
}

func cmdMCPTasksStatus(args []string) int {
	return cmdMCPTasksReport(args, false)
}

func cmdMCPTasksReport(args []string, doctor bool) int {
	fs := newFlagSet("mcp-tasks-status", flag.ContinueOnError)
	preflightPath := fs.String("preflight-path", "", "generated starter directory whose Task doctor runs with local deployment bindings (hosting doctor only)")
	appFlag := fs.String("app", "", "worker app slug (defaults to the linked project app)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) || fs.NArg() != 0 {
		return printErr("Invalid MCP task status flags", errors.New("unexpected positional arguments"))
	}
	if !doctor && *preflightPath != "" {
		return printErr("Invalid flags", errors.New("--preflight-path requires mcp doctor --hosting"))
	}
	slug, err := resolveAppFlagOrContext(*appFlag)
	if err != nil {
		return printErr("Could not resolve worker app", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	app, err := client.GetApp(ctx, slug)
	if err != nil {
		return printErr("Could not get worker app", err)
	}
	if !isWorkerApp(app) {
		return printErr("Invalid app mode", fmt.Errorf("app %q is not configured for worker execution", slug))
	}
	metrics, err := client.GetAppCustomMetrics(ctx, app.Slug)
	if err != nil {
		return printErr("Could not get worker custom metrics", err)
	}

	scaling := api.WorkerScaling{Metric: "queue_lag"}
	if app.Manifest.WorkerReplicas != nil {
		scaling = *app.Manifest.WorkerReplicas
	}
	configured := app.Manifest.WorkerReplicas != nil &&
		scaling.Metric == api.ScalingMetricCustom &&
		scaling.Name == mcpTasksOutstandingMetric && scaling.Target > 0 &&
		mcpTasksPolicyMatches(app.ScalingPolicy, scaling)
	result := mcpTasksStatusResult{
		AppSlug:               app.Slug,
		Configured:            configured,
		ScaleToZeroConfigured: configured && scaling.Min == 0,
		FreshnessSeconds:      metrics.FreshnessS,
		Scaling:               scaling,
		Metrics: []mcpTasksMetricStatus{
			{Name: mcpTasksOutstandingMetric},
			{Name: mcpTasksOldestAgeMetric},
			{Name: "mcp_tasks_running"},
			{Name: "mcp_tasks_capacity_waiting"},
			{Name: "mcp_tasks_retry_waiting"},
			{Name: "mcp_tasks_failed"},
			{Name: "mcp_tasks_active_workers"},
			{Name: "mcp_tasks_draining_workers"},
			{Name: "mcp_tasks_unsupported_handler_tasks"},
			{Name: "mcp_tasks_observer_heartbeat"},
		},
	}
	for _, metric := range metrics.Metrics {
		for i := range result.Metrics {
			if result.Metrics[i].Name != metric.Name {
				continue
			}
			observedAt := metric.ObservedAt
			result.Metrics[i].Present = true
			result.Metrics[i].Value = metric.Value
			result.Metrics[i].ObservedAt = &observedAt
			result.Metrics[i].Stale = metric.Stale
			if metric.Name == mcpTasksOutstandingMetric {
				result.OutstandingMetricFresh = !metric.Stale
			}
		}
	}
	result.diagnose()
	if doctor {
		return printMCPHostingDoctor(result, *preflightPath)
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	_, _ = fmt.Fprintf(osStdout, "MCP task scaling status for %s\n", result.AppSlug)
	if result.Configured {
		_, _ = fmt.Fprintln(osStdout, "  Policy: configured")
	} else {
		_, _ = fmt.Fprintln(osStdout, "  Policy: not configured for mcp_tasks_outstanding (run mcp tasks setup)")
	}
	_, _ = fmt.Fprintf(osStdout, "  Replicas: min=%d, max=%d; target %.0f tasks per worker\n", scaling.Min, scaling.Max, scaling.Target)
	for _, metric := range result.Metrics {
		if !metric.Present {
			_, _ = fmt.Fprintf(osStdout, "  %-32s not published\n", metric.Name)
			continue
		}
		freshness := "fresh"
		if metric.Stale {
			freshness = "stale"
		}
		_, _ = fmt.Fprintf(osStdout, "  %-32s %.2f (%s at %s)\n", metric.Name, metric.Value, freshness, metric.ObservedAt.UTC().Format(time.RFC3339))
	}
	_, _ = fmt.Fprintf(osStdout, "  Freshness window: %ds\n", result.FreshnessSeconds)
	for _, diagnostic := range result.Diagnostics {
		_, _ = fmt.Fprintf(osStdout, "  %s: %s\n", diagnostic.Code, diagnostic.Message)
	}
	if result.ScaleToZeroConfigured {
		if result.OutstandingMetricFresh {
			_, _ = fmt.Fprintln(osStdout, "  Scale-to-zero signal: policy is enabled and the backlog metric is fresh.")
		} else {
			_, _ = fmt.Fprintln(osStdout, "  Scale-to-zero signal: policy is enabled, but the backlog metric is missing or stale.")
		}
		_, _ = fmt.Fprintln(osStdout, "  Verify the separate always-on task observer is healthy; this status cannot confirm its deployment.")
	}
	return 0
}

// Diagnose only fresh observations. Missing telemetry does not imply zero work.
func (result *mcpTasksStatusResult) diagnose() {
	result.Diagnostics = make([]mcpTasksDiagnostic, 0)
	values := make(map[string]float64)
	var unavailable []string
	for _, metric := range result.Metrics {
		if metric.Present && !metric.Stale {
			values[metric.Name] = metric.Value
		} else if metric.Name != "mcp_tasks_observer_heartbeat" {
			unavailable = append(unavailable, metric.Name)
		}
	}
	add := func(code, message string) {
		result.Diagnostics = append(result.Diagnostics, mcpTasksDiagnostic{Code: code, Message: message})
	}
	workers, workersKnown := values["mcp_tasks_active_workers"]
	observer, observerKnown := values["mcp_tasks_observer_heartbeat"]
	result.WorkerHeartbeatsFresh = workersKnown && workers > 0
	result.ObserverHeartbeatFresh = observerKnown && observer == 1
	if len(unavailable) > 0 {
		add("telemetry_incomplete", "Missing or stale metrics: "+strings.Join(unavailable, ", ")+". Check the metrics publisher and database access; queue health is only partially known.")
	}
	if result.ScaleToZeroConfigured && !result.ObserverHeartbeatFresh {
		add("observer_health_unknown", "Scale-to-zero is configured, but the separate observer heartbeat is missing or stale. Check the always-on observer and its metrics credentials.")
	}
	if values["mcp_tasks_draining_workers"] > 0 {
		add("workers_draining", "Workers are finishing active Tasks and no longer claiming new work. Verify replacement workers are available during rollout.")
	}
	if values["mcp_tasks_capacity_waiting"] > 0 {
		add("execution_capacity", "Tasks are waiting for namespace or owner execution capacity. More replicas may not help; review the configured running limits and active leases.")
	}
	if values["mcp_tasks_retry_waiting"] > 0 {
		add("retry_delay", "Tasks are waiting for their persisted retry time. They remain outstanding but cannot run until the backoff expires.")
	}
	if values["mcp_tasks_failed"] > 0 {
		add("failed_tasks_retained", "Terminal failed Tasks are retained within TTL. Review worker logs and handler error classification; this gauge is not a cumulative failure rate.")
	}
	if result.WorkerHeartbeatsFresh && values["mcp_tasks_unsupported_handler_tasks"] > 0 {
		add("unsupported_handler", "Pending Tasks have no matching handler version in the live worker registry. Deploy a compatible worker or retain previous handler implementations.")
	}
	if values[mcpTasksOutstandingMetric] > 0 {
		if workersKnown && workers == 0 {
			add("no_active_workers", "Outstanding work has no live worker registration in the latest report. Check worker startup and autoscaling; zero workers can be intentional while idle or waiting for retries.")
		}
		running, runningKnown := values["mcp_tasks_running"]
		_, capacityKnown := values["mcp_tasks_capacity_waiting"]
		_, retriesKnown := values["mcp_tasks_retry_waiting"]
		_, handlersKnown := values["mcp_tasks_unsupported_handler_tasks"]
		if result.WorkerHeartbeatsFresh && runningKnown && running == 0 && capacityKnown && retriesKnown && handlersKnown && values["mcp_tasks_capacity_waiting"] == 0 && values["mcp_tasks_retry_waiting"] == 0 && values["mcp_tasks_unsupported_handler_tasks"] == 0 {
			add("idle_queue", "Ready work has no live execution lease in this snapshot. Check worker polling and logs for claim or database errors.")
		}
	}
}
