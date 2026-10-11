package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	mcpTasksOutstandingMetric             = "mcp_tasks_outstanding"
	mcpTasksOldestAgeMetric               = "mcp_tasks_oldest_age_seconds"
	mcpTasksWithoutCompatibleWorkerMetric = "mcp_tasks_without_compatible_worker"
	mcpTasksObserverHeartbeatMetric       = "mcp_tasks_observer_heartbeat"
)

func cmdMCPTasks(args []string) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		PrintUsage(os.Stderr,
			"usage: gregale mcp tasks <setup|status|observer> [flags]", "mcp")
		return 0
	}
	switch args[0] {
	case "setup":
		return cmdMCPTasksSetup(args[1:])
	case "status":
		return cmdMCPTasksStatus(args[1:])
	case "observer":
		return cmdMCPTasksObserver(args[1:])
	default:
		return printErr("Unknown MCP tasks command", fmt.Errorf("%q", args[0]))
	}
}

type mcpTasksSetupResult struct {
	AppSlug                string                    `json:"app_slug"`
	Applied                bool                      `json:"applied"`
	Changed                bool                      `json:"changed"`
	NeedsUpdate            bool                      `json:"needs_update"`
	Current                *api.WorkerScaling        `json:"current,omitempty"`
	Proposed               api.WorkerScaling         `json:"proposed"`
	ObserverApp            mcpTasksObserverReadiness `json:"observer_app"`
	ObserverHeartbeatFresh bool                      `json:"observer_heartbeat_fresh"`
}

func cmdMCPTasksSetup(args []string) int {
	fs := newFlagSet("mcp-tasks-setup", flag.ContinueOnError)
	appFlag := fs.String("app", "", "worker app slug (defaults to the linked project app)")
	minFlag := fs.Int("min", -1, "minimum worker replicas; min=0 requires a ready linked observer and fresh heartbeat")
	maxFlag := fs.Int("max", -1, "maximum worker replicas; default 10")
	targetFlag := fs.Float64("target", -1, "outstanding tasks per worker; default 4")
	observerAppFlag := fs.String("observer-app", "", "separate always-on worker app that publishes task metrics")
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
	if *observerAppFlag != "" {
		if !api.ValidAppSlug(*observerAppFlag) {
			return printErr("Invalid observer app slug", fmt.Errorf("%q is not a valid Gregale app slug", *observerAppFlag))
		}
		proposed.ObserverAppSlug = *observerAppFlag
	}
	if proposed.Min < 0 || proposed.Max < 1 || proposed.Min > proposed.Max ||
		proposed.Target <= 0 {
		return printErr("Invalid MCP task scaling policy", errors.New("replicas must satisfy 0 <= min <= max, max must be at least 1, and target must be greater than zero"))
	}
	if problem := api.ValidateCustomMetricName(proposed.Name); problem != nil {
		return printErr("Invalid MCP task metric", errors.New(problem.Detail))
	}
	observer := mcpTasksObserverReadiness{AppSlug: proposed.ObserverAppSlug, Violations: []string{}}
	if proposed.Min == 0 || proposed.ObserverAppSlug != "" {
		observer = loadMCPTasksObserverReadiness(ctx, client, app, proposed.ObserverAppSlug)
	}
	observerHeartbeatFresh := false
	var observerHeartbeatErr error
	if proposed.Min == 0 && observer.Ready {
		metrics, metricErr := client.GetAppCustomMetrics(ctx, app.Slug)
		observerHeartbeatErr = metricErr
		if observerHeartbeatErr == nil {
			for _, metric := range metrics.Metrics {
				if metric.Name == mcpTasksObserverHeartbeatMetric {
					observerHeartbeatFresh = !metric.Stale && metric.Value == 1
					break
				}
			}
		}
	}
	if *apply && proposed.Min == 0 {
		if !observer.Ready {
			return printErr("MCP task observer is not ready", fmt.Errorf("configure scale-to-zero only after a separate always-on observer is ready: %s", strings.Join(observer.Violations, ", ")))
		}
		if observerHeartbeatErr != nil {
			return printErr("Could not verify MCP task observer heartbeat", observerHeartbeatErr)
		}
		if !observerHeartbeatFresh {
			return printErr("MCP task observer heartbeat is not fresh", errors.New("wait for the linked observer to publish a fresh mcp_tasks_observer_heartbeat with value 1, then retry"))
		}
	}

	current := app.Manifest.WorkerReplicas
	needsUpdate := current == nil || *current != proposed || !mcpTasksPolicyMatches(app.ScalingPolicy, proposed)
	if proposed.Min == 0 && proposed.ObserverAppSlug == "" {
		needsUpdate = true
	}
	result := mcpTasksSetupResult{
		AppSlug: app.Slug, Applied: *apply, NeedsUpdate: needsUpdate,
		Current: current, Proposed: proposed, ObserverApp: observer,
		ObserverHeartbeatFresh: observerHeartbeatFresh,
	}
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
	if result.Proposed.ObserverAppSlug != "" {
		_, _ = fmt.Fprintf(osStdout, "  Observer: %s (ready=%t, min=%d, running=%d)\n", result.ObserverApp.AppSlug, result.ObserverApp.Ready, result.ObserverApp.MinReplicas, result.ObserverApp.Replicas)
		if result.Proposed.Min == 0 {
			_, _ = fmt.Fprintf(osStdout, "  Observer heartbeat: fresh=%t\n", result.ObserverHeartbeatFresh)
		}
		for _, violation := range result.ObserverApp.Violations {
			_, _ = fmt.Fprintf(osStdout, "  Observer issue: %s\n", violation)
		}
	} else if result.Proposed.Min == 0 {
		_, _ = fmt.Fprintln(osStdout, "  Observer: not linked; pass --observer-app <slug> before applying scale-to-zero.")
	}
	if result.NeedsUpdate {
		if result.Proposed.Min == 0 && result.Proposed.ObserverAppSlug == "" {
			_, _ = fmt.Fprintln(osStdout, "  Pass --observer-app <slug> before applying this policy.")
		} else {
			_, _ = fmt.Fprintln(osStdout, "  Re-run with --apply to save this policy.")
		}
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

type mcpTasksStatusResult struct {
	AppSlug                         string                          `json:"app_slug"`
	Configured                      bool                            `json:"configured"`
	ScaleToZeroConfigured           bool                            `json:"scale_to_zero_configured"`
	OutstandingMetricFresh          bool                            `json:"outstanding_metric_fresh"`
	ObserverHeartbeatFresh          *bool                           `json:"observer_heartbeat_fresh,omitempty"`
	ObserverApp                     *mcpTasksObserverReadiness      `json:"observer_app,omitempty"`
	FreshnessSeconds                int                             `json:"freshness_seconds"`
	Scaling                         api.WorkerScaling               `json:"scaling"`
	Metrics                         []mcpTasksMetricStatus          `json:"metrics"`
	Health                          string                          `json:"health"`
	ChecksEnabled                   bool                            `json:"checks_enabled"`
	MaxOldestAgeSeconds             *float64                        `json:"max_oldest_age_seconds,omitempty"`
	MaxWithoutCompatibleWorkerTasks *int64                          `json:"max_without_compatible_worker_tasks,omitempty"`
	RequireFreshMetrics             bool                            `json:"require_fresh_metrics"`
	SustainForSeconds               float64                         `json:"sustain_for_seconds,omitempty"`
	Sustained                       bool                            `json:"sustained"`
	HandlerCompatibility            *api.MCPTaskCompatibilityReportResponse `json:"handler_compatibility,omitempty"`
	Violations                      []string                        `json:"violations"`
}

type mcpTasksObserverReadiness struct {
	AppSlug                string                        `json:"app_slug,omitempty"`
	AppID                  string                        `json:"app_id,omitempty"`
	Linked                 bool                          `json:"linked"`
	Ready                  bool                          `json:"ready"`
	Status                 string                        `json:"status,omitempty"`
	DeploymentAvailability api.AppDeploymentAvailability `json:"deployment_availability,omitempty"`
	MinReplicas            int                           `json:"min_replicas"`
	Replicas               int                           `json:"replicas"`
	Violations             []string                      `json:"violations"`
}

func loadMCPTasksObserverReadiness(ctx context.Context, client *api.Client, worker api.AppResponse, observerSlug string) mcpTasksObserverReadiness {
	status := mcpTasksObserverReadiness{
		AppSlug:    observerSlug,
		Linked:     observerSlug != "",
		Violations: []string{},
	}
	addViolation := func(reason string) { status.Violations = append(status.Violations, reason) }
	if observerSlug == "" {
		addViolation("observer_app_not_linked")
		return status
	}
	if observerSlug == worker.Slug {
		addViolation("observer_app_must_be_separate")
		return status
	}
	observer, err := client.GetApp(ctx, observerSlug)
	if err != nil {
		addViolation("observer_app_unavailable")
		return status
	}
	status.AppSlug = observer.Slug
	status.AppID = observer.ID
	status.Status = observer.Status
	status.DeploymentAvailability = observer.DeploymentAvailability
	if observer.Slug != observerSlug {
		addViolation("observer_app_slug_mismatch")
	}
	if observer.ID == "" || worker.ID == "" || observer.ID == worker.ID {
		addViolation("observer_app_identity_not_distinct")
	}
	if !isWorkerApp(observer) {
		addViolation("observer_app_not_worker_mode")
	}
	if observer.Status != "active" {
		addViolation("observer_app_not_active")
	}
	if observer.DeploymentAvailability != api.AppDeploymentAvailabilityLive {
		addViolation("observer_app_has_no_live_deployment")
	}
	if observer.Manifest.WorkerReplicas == nil || observer.Manifest.WorkerReplicas.Min < 1 {
		addViolation("observer_app_min_replicas_must_be_positive")
	} else {
		status.MinReplicas = observer.Manifest.WorkerReplicas.Min
	}
	instances, err := client.ListInstancesWithHistory(ctx, observer.Slug, false)
	if err != nil {
		addViolation("observer_app_instances_unavailable")
	} else {
		for _, instance := range instances {
			if humanizeInstanceState(instance.State) == "running" {
				status.Replicas++
			}
		}
		if status.Replicas < 1 {
			addViolation("observer_app_has_no_running_replicas")
		}
	}
	status.Ready = len(status.Violations) == 0
	return status
}

func cmdMCPTasksStatus(args []string) int {
	fs := newFlagSet("mcp-tasks-status", flag.ContinueOnError)
	appFlag := fs.String("app", "", "worker app slug (defaults to the linked project app)")
	details := fs.Bool("details", false, "show waiting counts from the observer's latest handler-compatibility report")
	maxOldestAge := fs.Duration("max-oldest-age", 0, "fail when oldest queued task age exceeds this duration")
	maxWithoutCompatibleWorker := fs.Int64("max-without-compatible-worker", -1, "fail when more than this many queued tasks lack a live compatible worker")
	requireFresh := fs.Bool("require-fresh", false, "fail when required MCP task metrics are missing or stale")
	sustainFor := fs.Duration("sustain-for", 0, "require an active violation to persist this long before failing")
	pollInterval := fs.Duration("poll-interval", 15*time.Second, "poll interval while waiting for a sustained violation")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) || fs.NArg() != 0 {
		return printErr("Invalid MCP task status flags", errors.New("unexpected positional arguments"))
	}
	if *maxOldestAge < 0 || *maxWithoutCompatibleWorker < -1 || *sustainFor < 0 || *pollInterval <= 0 {
		return printErr("Invalid MCP task status thresholds", errors.New("specified thresholds and durations must be non-negative; --poll-interval must be greater than zero"))
	}
	checksEnabled := *maxOldestAge > 0 || *maxWithoutCompatibleWorker >= 0 || *requireFresh
	if *sustainFor > 0 && !checksEnabled {
		return printErr("Invalid MCP task status thresholds", errors.New("--sustain-for requires a metric threshold or --require-fresh"))
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
	buildResult := func(metrics api.CustomMetricListResponse) mcpTasksStatusResult {
		scaleToZeroConfigured := configured && scaling.Min == 0
		result := mcpTasksStatusResult{
			AppSlug:               app.Slug,
			Configured:            configured,
			ScaleToZeroConfigured: scaleToZeroConfigured,
			FreshnessSeconds:      metrics.FreshnessS,
			Scaling:               scaling,
			Health:                "unconfigured",
			ChecksEnabled:         checksEnabled,
			RequireFreshMetrics:   *requireFresh,
			Violations:            []string{},
			Metrics: []mcpTasksMetricStatus{
				{Name: mcpTasksOutstandingMetric},
				{Name: mcpTasksOldestAgeMetric},
				{Name: mcpTasksWithoutCompatibleWorkerMetric},
			},
		}
		if result.ScaleToZeroConfigured || scaling.ObserverAppSlug != "" {
			observer := loadMCPTasksObserverReadiness(ctx, client, app, scaling.ObserverAppSlug)
			result.ObserverApp = &observer
		}
		if result.ScaleToZeroConfigured {
			result.Metrics = append(result.Metrics, mcpTasksMetricStatus{Name: mcpTasksObserverHeartbeatMetric})
			fresh := false
			result.ObserverHeartbeatFresh = &fresh
		}
		if *maxOldestAge > 0 {
			seconds := maxOldestAge.Seconds()
			result.MaxOldestAgeSeconds = &seconds
		}
		if *maxWithoutCompatibleWorker >= 0 {
			threshold := *maxWithoutCompatibleWorker
			result.MaxWithoutCompatibleWorkerTasks = &threshold
		}
		if *sustainFor > 0 {
			result.SustainForSeconds = sustainFor.Seconds()
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
					result.OutstandingMetricFresh = result.Metrics[i].Present && !metric.Stale
				}
				if metric.Name == mcpTasksObserverHeartbeatMetric {
					fresh := mcpTasksObserverHeartbeatIsFresh(result.Metrics[i])
					result.ObserverHeartbeatFresh = &fresh
				}
			}
		}
		applyMCPTasksStatusChecks(&result, *maxOldestAge, *maxWithoutCompatibleWorker, *requireFresh)
		return result
	}
	result := buildResult(metrics)
	if *sustainFor > 0 && len(result.Violations) > 0 {
		breachStarted := time.Now()
		for time.Since(breachStarted) < *sustainFor {
			remaining := *sustainFor - time.Since(breachStarted)
			delay := *pollInterval
			if remaining < delay {
				delay = remaining
			}
			timer := time.NewTimer(delay)
			<-timer.C
			metrics, err = client.GetAppCustomMetrics(ctx, app.Slug)
			if err != nil {
				return printErr("Could not refresh worker custom metrics", err)
			}
			result = buildResult(metrics)
			if len(result.Violations) == 0 {
				break
			}
		}
		result.Sustained = len(result.Violations) > 0 && time.Since(breachStarted) >= *sustainFor
	}
	if *details {
		detailsCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		handlerCompatibility, detailErr := client.GetAppMCPTaskCompatibilityReport(detailsCtx, app.Slug)
		cancel()
		if detailErr != nil {
			return printErr("Could not load MCP task handler details", detailErr)
		}
		result.HandlerCompatibility = &handlerCompatibility
	}
	if jsonOutput {
		if code := jsonOut(writeJSON(result)); code != 0 {
			return code
		}
	} else {
		printMCPTasksStatus(osStdout, result, scaling)
	}
	if len(result.Violations) > 0 {
		return 1
	}
	return 0
}

func mcpTasksObserverHeartbeatIsFresh(metric mcpTasksMetricStatus) bool {
	return metric.Name == mcpTasksObserverHeartbeatMetric && metric.Present && !metric.Stale && metric.Value == 1
}

func applyMCPTasksStatusChecks(result *mcpTasksStatusResult, maxOldestAge time.Duration, maxWithoutCompatibleWorker int64, requireFresh bool) {
	result.Health = "unconfigured"
	if !result.ChecksEnabled {
		return
	}
	result.Health = "healthy"
	addViolation := func(message string) {
		for _, existing := range result.Violations {
			if existing == message {
				return
			}
		}
		result.Violations = append(result.Violations, message)
	}
	metricByName := make(map[string]mcpTasksMetricStatus, len(result.Metrics))
	for _, metric := range result.Metrics {
		metricByName[metric.Name] = metric
	}
	checkFresh := func(name string) {
		metric := metricByName[name]
		if !metric.Present {
			addViolation(name + " is not published")
		} else if metric.Stale {
			addViolation(name + " is stale")
		}
	}
	if requireFresh {
		checkFresh(mcpTasksOutstandingMetric)
		checkFresh(mcpTasksOldestAgeMetric)
		checkFresh(mcpTasksWithoutCompatibleWorkerMetric)
		if result.ScaleToZeroConfigured {
			if result.ObserverApp == nil || !result.ObserverApp.Linked {
				addViolation("observer app is not linked")
			} else if !result.ObserverApp.Ready {
				addViolation("observer app is not ready")
			}
			metric := metricByName[mcpTasksObserverHeartbeatMetric]
			if !metric.Present {
				addViolation(mcpTasksObserverHeartbeatMetric + " is not published")
			} else if metric.Stale {
				addViolation(mcpTasksObserverHeartbeatMetric + " is stale")
			} else if !mcpTasksObserverHeartbeatIsFresh(metric) {
				addViolation(mcpTasksObserverHeartbeatMetric + " is not reporting a healthy value")
			}
		}
	}
	if maxOldestAge > 0 {
		metric := metricByName[mcpTasksOldestAgeMetric]
		if !metric.Present {
			addViolation(mcpTasksOldestAgeMetric + " is not published")
		} else if metric.Stale {
			addViolation(mcpTasksOldestAgeMetric + " is stale")
		} else if metric.Value > maxOldestAge.Seconds() {
			addViolation(fmt.Sprintf("%s is %.2fs; maximum is %s", mcpTasksOldestAgeMetric, metric.Value, maxOldestAge))
		}
	}
	if maxWithoutCompatibleWorker >= 0 {
		metric := metricByName[mcpTasksWithoutCompatibleWorkerMetric]
		if !metric.Present {
			addViolation(mcpTasksWithoutCompatibleWorkerMetric + " is not published")
		} else if metric.Stale {
			addViolation(mcpTasksWithoutCompatibleWorkerMetric + " is stale")
		} else if metric.Value > float64(maxWithoutCompatibleWorker) {
			addViolation(fmt.Sprintf("%s is %.0f; maximum is %d", mcpTasksWithoutCompatibleWorkerMetric, metric.Value, maxWithoutCompatibleWorker))
		}
	}
	if len(result.Violations) > 0 {
		result.Health = "degraded"
	}
}

func printMCPTasksStatus(w io.Writer, result mcpTasksStatusResult, scaling api.WorkerScaling) {
	_, _ = fmt.Fprintf(w, "MCP task scaling status for %s\n", result.AppSlug)
	if result.Configured {
		_, _ = fmt.Fprintln(w, "  Policy: configured")
	} else {
		_, _ = fmt.Fprintln(w, "  Policy: not configured for mcp_tasks_outstanding (run mcp tasks setup)")
	}
	_, _ = fmt.Fprintf(w, "  Replicas: min=%d, max=%d; target %.0f tasks per worker\n", scaling.Min, scaling.Max, scaling.Target)
	for _, metric := range result.Metrics {
		if !metric.Present {
			_, _ = fmt.Fprintf(w, "  %-32s not published\n", metric.Name)
			continue
		}
		freshness := "fresh"
		if metric.Stale {
			freshness = "stale"
		}
		_, _ = fmt.Fprintf(w, "  %-32s %.2f (%s at %s)\n", metric.Name, metric.Value, freshness, metric.ObservedAt.UTC().Format(time.RFC3339))
	}
	_, _ = fmt.Fprintf(w, "  Freshness window: %ds\n", result.FreshnessSeconds)
	if report := result.HandlerCompatibility; report != nil {
		if !report.Published {
			_, _ = fmt.Fprintln(w, "  Handler compatibility report: not published by the observer")
		} else {
			freshness := "fresh"
			if report.Stale {
				freshness = "stale"
			}
			if report.ObservedAt != nil {
				_, _ = fmt.Fprintf(w, "  Handler compatibility report (%s, observed at %s):\n", freshness, report.ObservedAt.UTC().Format(time.RFC3339))
			} else {
				_, _ = fmt.Fprintf(w, "  Handler compatibility report (%s):\n", freshness)
			}
			if len(report.Handlers) == 0 {
				_, _ = fmt.Fprintln(w, "    No outstanding tasks need a handler")
			}
			for _, handler := range report.Handlers {
				_, _ = fmt.Fprintf(w, "    tool=%q, handler_version=%q: waiting=%d, live compatible workers=%d\n", handler.ToolName, handler.HandlerVersion, handler.WaitingTasks, handler.LiveCompatibleWorkers)
				if handler.UnknownDeploymentWorkers > 0 {
					_, _ = fmt.Fprintf(w, "      deployment identity unavailable for %d worker(s)\n", handler.UnknownDeploymentWorkers)
				}
				for _, deployment := range handler.Deployments {
					_, _ = fmt.Fprintf(w, "      deployment=%s: workers=%d\n", deployment.DeploymentID, deployment.Workers)
				}
			}
			if report.Truncated {
				_, _ = fmt.Fprintln(w, "    Additional task handler or deployment data omitted; rollout compatibility gate will hold cutovers")
			}
		}
	}
	_, _ = fmt.Fprintf(w, "  Health: %s\n", result.Health)
	if result.ChecksEnabled {
		if result.MaxOldestAgeSeconds != nil {
			_, _ = fmt.Fprintf(w, "  Oldest-age limit: %s\n", time.Duration(*result.MaxOldestAgeSeconds*float64(time.Second)))
		}
		if result.MaxWithoutCompatibleWorkerTasks != nil {
			_, _ = fmt.Fprintf(w, "  No-compatible-worker limit: %d tasks\n", *result.MaxWithoutCompatibleWorkerTasks)
		}
		if result.RequireFreshMetrics {
			_, _ = fmt.Fprintln(w, "  Fresh metrics required: yes")
		}
		if result.SustainForSeconds > 0 {
			_, _ = fmt.Fprintf(w, "  Sustained violation: %t (required %s)\n", result.Sustained, time.Duration(result.SustainForSeconds*float64(time.Second)))
		}
		for _, violation := range result.Violations {
			_, _ = fmt.Fprintf(w, "  Violation: %s\n", violation)
		}
	}
	if result.ScaleToZeroConfigured {
		if result.ObserverApp != nil {
			observerName := result.ObserverApp.AppSlug
			if !result.ObserverApp.Linked {
				observerName = "not linked"
			}
			_, _ = fmt.Fprintf(w, "  Observer app: %s (ready=%t, min=%d, running=%d)\n", observerName, result.ObserverApp.Ready, result.ObserverApp.MinReplicas, result.ObserverApp.Replicas)
			for _, violation := range result.ObserverApp.Violations {
				_, _ = fmt.Fprintf(w, "  Observer issue: %s\n", violation)
			}
		}
		if result.OutstandingMetricFresh && result.ObserverHeartbeatFresh != nil && *result.ObserverHeartbeatFresh {
			_, _ = fmt.Fprintln(w, "  Scale-to-zero signal: backlog metric and dedicated observer heartbeat are fresh.")
		} else {
			_, _ = fmt.Fprintln(w, "  Scale-to-zero signal: backlog metric or dedicated observer heartbeat is missing, stale, or unhealthy.")
		}
	}
}
