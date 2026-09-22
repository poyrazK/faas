// commands_workers.go — Developer CLI for background worker pools.
//
// Subcommands:
//   gregale workers [list]                 List all background worker pools and their scaling status
//   gregale workers status [<app>]          Detailed status: replicas, queue lag, broker, draining, instances, DLQ
//   gregale workers logs [<app>] [flags]   Tail logs for background worker instances
//   gregale workers scale <app> [flags]    Adjust worker scaling bounds and graceful drain parameters
//
// Wire contract and state mapping:
//   - An app is a worker if Manifest.EffectiveExecutionMode() == "worker" or WorkloadClass == "worker"
//   - Autoscaling parameters live in Manifest.WorkerReplicas (Min, Max, Metric, Target)
//   - Draining parameters live in Manifest.StopGracePeriodS and Manifest.StopSignal
//   - Queue telemetry and DLQ status query attached triggers via client.GetTriggers / GetTriggersIdMetrics

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func isWorkerApp(a api.AppResponse) bool {
	if a.Manifest.EffectiveExecutionMode() == api.ExecutionModeWorker {
		return true
	}
	if a.WorkloadClass == "worker" {
		return true
	}
	return false
}

// WorkerPoolSummary describes one worker pool in list output.
type WorkerPoolSummary struct {
	Slug         string  `json:"slug"`
	Status       string  `json:"status"`
	Replicas     int     `json:"replicas"`
	MinReplicas  int     `json:"min_replicas"`
	MaxReplicas  int     `json:"max_replicas"`
	Metric       string  `json:"metric"`
	Target       float64 `json:"target"`
	QueueLag     int64   `json:"queue_lag"`
	Broker       string  `json:"broker,omitempty"`
	Source       string  `json:"source,omitempty"`
	DrainTimeout int     `json:"drain_timeout_s"`
	StopSignal   string  `json:"stop_signal"`
}

// WorkerInstanceDetail describes one running worker instance in status output.
type WorkerInstanceDetail struct {
	ID        string `json:"id"`
	State     string `json:"state"`
	RAMMB     int    `json:"ram_mb"`
	StartedAt string `json:"started_at"`
	Uptime    string `json:"uptime"`
	HostIP    string `json:"host_ip,omitempty"`
}

// WorkerPoolStatus describes detailed status for a single worker pool.
type WorkerPoolStatus struct {
	Slug         string                 `json:"slug"`
	AppID        string                 `json:"app_id"`
	Status       string                 `json:"status"`
	RAMMB        int                    `json:"ram_mb"`
	VCPU         int                    `json:"vcpu"`
	Replicas     int                    `json:"replicas"`
	MinReplicas  int                    `json:"min_replicas"`
	MaxReplicas  int                    `json:"max_replicas"`
	Metric       string                 `json:"metric"`
	Target       float64                `json:"target"`
	QueueLag     int64                  `json:"queue_lag"`
	DrainTimeout int                    `json:"drain_timeout_s"`
	StopSignal   string                 `json:"stop_signal"`
	Restart      string                 `json:"restart_policy"`
	BrokerKind   string                 `json:"broker_kind,omitempty"`
	BrokerSource string                 `json:"broker_source,omitempty"`
	DLQCount     int64                  `json:"dlq_count"`
	RetryCount   int64                  `json:"retry_count"`
	Instances    []WorkerInstanceDetail `json:"instances"`
}

func cmdWorkers(args []string) int {
	if len(args) == 0 {
		return cmdWorkersList([]string{})
	}
	if hasHelpFlag(args) && len(args) == 1 {
		PrintUsage(osStdout, "usage: gregale workers <list|status|logs|scale> [args]", "workers")
		return 0
	}

	switch args[0] {
	case "list", "ls":
		return cmdWorkersList(args[1:])
	case "status":
		return cmdWorkersStatus(args[1:])
	case "logs":
		return cmdWorkersLogs(args[1:])
	case "scale":
		return cmdWorkersScale(args[1:])
	default:
		// If the first argument is a flag (e.g. --json), pass to list
		if strings.HasPrefix(args[0], "-") {
			return cmdWorkersList(args)
		}
		// Convenience: `gregale workers <slug>` implies `gregale workers status <slug>`
		return cmdWorkersStatus(args)
	}
}

func extractTriggerSource(t api.Trigger) (kind string, source string) {
	kind = string(t.Kind)
	if len(t.Config) == 0 {
		return kind, ""
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(t.Config, &raw); err != nil {
		return kind, ""
	}
	for _, k := range []string{"queue", "topic", "stream", "subject", "queue_url"} {
		if val, ok := raw[k].(string); ok && val != "" {
			return kind, val
		}
	}
	return kind, ""
}

func cmdWorkersList(args []string) int {
	usage := "usage: gregale workers [list] [--json]"
	fs := newFlagSet("workers list", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		PrintUsage(osStderr, usage, "workers")
		return 1
	}

	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}

	ctx := context.Background()
	apps, err := client.ListApps(ctx)
	if err != nil {
		return printErr("Could not list apps", err)
	}

	var workerSummaries []WorkerPoolSummary
	for _, a := range apps {
		if !isWorkerApp(a) {
			continue
		}

		summary := WorkerPoolSummary{
			Slug:       a.Slug,
			Status:     a.Status,
			Metric:     "queue_lag",
			StopSignal: "SIGTERM",
		}
		if a.Manifest.WorkerReplicas != nil {
			summary.MinReplicas = a.Manifest.WorkerReplicas.Min
			summary.MaxReplicas = a.Manifest.WorkerReplicas.Max
			if a.Manifest.WorkerReplicas.Metric != "" {
				summary.Metric = a.Manifest.WorkerReplicas.Metric
			}
			summary.Target = a.Manifest.WorkerReplicas.Target
		}
		if a.Manifest.StopGracePeriod > 0 {
			summary.DrainTimeout = int(a.Manifest.StopGracePeriod.Seconds())
		}
		if a.Manifest.StopSignal != "" {
			summary.StopSignal = a.Manifest.StopSignal
		}

		// Count active instances
		instances, err := client.ListInstancesWithHistory(ctx, a.Slug, false)
		if err == nil {
			for _, ins := range instances {
				if humanizeInstanceState(ins.State) == "running" {
					summary.Replicas++
				}
			}
		}

		// Check trigger source and pending lag
		triggers, err := client.GetTriggers(ctx, a.ID, "")
		if err == nil && len(triggers) > 0 {
			t := triggers[0]
			bKind, bSrc := extractTriggerSource(t)
			summary.Broker = bKind
			summary.Source = bSrc

			metrics, mErr := client.GetTriggersIdMetrics(ctx, t.ID)
			if mErr == nil {
				summary.QueueLag = int64(metrics.PendingCount)
			}
		}

		workerSummaries = append(workerSummaries, summary)
	}

	if jsonOutput {
		return jsonOut(writeJSON(workerSummaries))
	}

	if len(workerSummaries) == 0 {
		_, _ = fmt.Fprintln(osStdout, "No background workers found.")
		_, _ = fmt.Fprintln(osStdout, "Deploy one by adding a 'worker:' section to gregale.yaml and running 'gregale deploy'.")
		return 0
	}

	_, _ = fmt.Fprintf(osStdout, "%-22s %-12s %-10s %-12s %-10s %-24s %s\n",
		"WORKER POOL", "REPLICAS", "SCALING", "METRIC", "TARGET", "SOURCE", "STATUS")
	for _, w := range workerSummaries {
		replicasStr := fmt.Sprintf("%d running", w.Replicas)
		scalingStr := fmt.Sprintf("%d..%d", w.MinReplicas, w.MaxReplicas)
		sourceStr := "-"
		if w.Broker != "" {
			if w.Source != "" {
				sourceStr = fmt.Sprintf("%s:%s", w.Broker, w.Source)
			} else {
				sourceStr = w.Broker
			}
		}
		targetStr := "-"
		if w.Target > 0 {
			targetStr = fmt.Sprintf("%.0f", w.Target)
		}

		statusStr := w.Status
		if w.Replicas == 0 && w.Status == "running" {
			statusStr = "idle (0 replicas)"
		}

		_, _ = fmt.Fprintf(osStdout, "%-22s %-12s %-10s %-12s %-10s %-24s %s\n",
			w.Slug, replicasStr, scalingStr, w.Metric, targetStr, sourceStr, statusStr)
	}
	return 0
}

func cmdWorkersStatus(args []string) int {
	usage := "usage: gregale workers status [<app>] [--json]"
	fs := newFlagSet("workers status", flag.ContinueOnError)
	appFlag := fs.String("app", "", "app slug")
	if err := fs.Parse(args); err != nil {
		PrintUsage(osStderr, usage, "workers")
		return 1
	}

	slug := *appFlag
	if slug == "" && fs.NArg() > 0 {
		slug = fs.Arg(0)
	}

	var resolveErr error
	slug, resolveErr = resolveAppFlagOrContext(slug)
	if resolveErr != nil {
		if errors.Is(resolveErr, errProjectContextNotFound) {
			PrintUsage(osStderr, "usage: gregale workers status <app> (or run inside a linked project directory)", "workers")
			return 1
		}
		return printErr("Could not resolve app slug", resolveErr)
	}

	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}

	ctx := context.Background()
	app, err := client.GetApp(ctx, slug)
	if err != nil {
		return printErr("Could not get app details", err)
	}

	if !isWorkerApp(app) {
		return printErr("Invalid app mode", fmt.Errorf("app %q has execution_mode=%q (not a worker). Use 'gregale status' or 'gregale ps' for HTTP apps", slug, app.Manifest.EffectiveExecutionMode()))
	}

	status := WorkerPoolStatus{
		Slug:         app.Slug,
		AppID:        app.ID,
		Status:       app.Status,
		RAMMB:        app.RAMMB,
		VCPU:         app.VCPU,
		Metric:       "queue_lag",
		StopSignal:   "SIGTERM",
		DrainTimeout: 30,
		Restart:      "always",
	}

	if app.Manifest.RestartPolicy != "" {
		status.Restart = app.Manifest.RestartPolicy
	}
	if app.Manifest.WorkerReplicas != nil {
		status.MinReplicas = app.Manifest.WorkerReplicas.Min
		status.MaxReplicas = app.Manifest.WorkerReplicas.Max
		if app.Manifest.WorkerReplicas.Metric != "" {
			status.Metric = app.Manifest.WorkerReplicas.Metric
		}
		status.Target = app.Manifest.WorkerReplicas.Target
	}
	if app.Manifest.StopGracePeriod > 0 {
		status.DrainTimeout = int(app.Manifest.StopGracePeriod.Seconds())
	}
	if app.Manifest.StopSignal != "" {
		status.StopSignal = app.Manifest.StopSignal
	}

	// Fetch instances
	instances, err := client.ListInstancesWithHistory(ctx, slug, false)
	if err == nil {
		for _, ins := range instances {
			hState := humanizeInstanceState(ins.State)
			if hState == "running" {
				status.Replicas++
			}
			uptimeStr := "-"
			if ins.StartedAt != "" {
				if t, parseErr := time.Parse(time.RFC3339, ins.StartedAt); parseErr == nil {
					uptimeStr = time.Since(t).Truncate(time.Second).String()
				}
			}
			status.Instances = append(status.Instances, WorkerInstanceDetail{
				ID:        ins.ID,
				State:     hState,
				RAMMB:     ins.RAMMB,
				StartedAt: ins.StartedAt,
				Uptime:    uptimeStr,
				HostIP:    ins.HostIP,
			})
		}
	}

	// Fetch triggers
	triggers, err := client.GetTriggers(ctx, app.ID, "")
	if err == nil && len(triggers) > 0 {
		t := triggers[0]
		bKind, bSrc := extractTriggerSource(t)
		status.BrokerKind = bKind
		status.BrokerSource = bSrc

		metrics, mErr := client.GetTriggersIdMetrics(ctx, t.ID)
		if mErr == nil {
			status.QueueLag = int64(metrics.PendingCount)
			status.DLQCount = int64(metrics.DeadLetterCount)
			status.RetryCount = int64(metrics.RetryCount)
		}
	}

	if jsonOutput {
		return jsonOut(writeJSON(status))
	}

	_, _ = fmt.Fprintf(osStdout, "Worker Pool:  %s\n", status.Slug)
	_, _ = fmt.Fprintf(osStdout, "Status:       %s\n", status.Status)
	_, _ = fmt.Fprintf(osStdout, "Resources:    %d MB RAM, %d vCPU\n", status.RAMMB, status.VCPU)
	_, _ = fmt.Fprintf(osStdout, "Lifecycle:    drain timeout: %ds, stop signal: %s, restart: %s\n\n",
		status.DrainTimeout, status.StopSignal, status.Restart)

	_, _ = fmt.Fprintln(osStdout, "Autoscaling:")
	_, _ = fmt.Fprintf(osStdout, "  Replicas:   %d running (min: %d, max: %d)\n", status.Replicas, status.MinReplicas, status.MaxReplicas)
	_, _ = fmt.Fprintf(osStdout, "  Metric:     %s\n", status.Metric)
	if status.Target > 0 {
		_, _ = fmt.Fprintf(osStdout, "  Target:     %.0f messages/worker\n", status.Target)
	}
	if status.QueueLag > 0 || status.BrokerKind != "" {
		_, _ = fmt.Fprintf(osStdout, "  Backlog:    %d messages pending\n", status.QueueLag)
	}
	_, _ = fmt.Fprintln(osStdout)

	if status.BrokerKind != "" {
		_, _ = fmt.Fprintln(osStdout, "Message Source:")
		_, _ = fmt.Fprintf(osStdout, "  Broker:     %s\n", status.BrokerKind)
		if status.BrokerSource != "" {
			_, _ = fmt.Fprintf(osStdout, "  Target:     %s\n", status.BrokerSource)
		}
		if status.DLQCount > 0 {
			_, _ = fmt.Fprintf(osStdout, "  DLQ:        %d dead-letter messages ⚠️\n", status.DLQCount)
		} else {
			_, _ = fmt.Fprintf(osStdout, "  DLQ:        0 dead-letter messages (healthy)\n")
		}
		_, _ = fmt.Fprintln(osStdout)
	}

	if len(status.Instances) == 0 {
		_, _ = fmt.Fprintln(osStdout, "Active Instances: 0 (worker pool is scaled to zero / parked)")
	} else {
		_, _ = fmt.Fprintf(osStdout, "Active Instances (%d):\n", len(status.Instances))
		_, _ = fmt.Fprintf(osStdout, "  %-36s %-12s %-8s %-20s %s\n", "INSTANCE ID", "STATE", "RAM_MB", "STARTED", "UPTIME")
		for _, ins := range status.Instances {
			started := ins.StartedAt
			if len(started) > 19 {
				started = started[:19]
			}
			_, _ = fmt.Fprintf(osStdout, "  %-36s %-12s %-8d %-20s %s\n",
				ins.ID, ins.State, ins.RAMMB, started, ins.Uptime)
		}
	}

	if status.DLQCount > 0 {
		_, _ = fmt.Fprintln(osStdout)
		PrintFail(osStderr, "Warning: %d dead-letter messages detected. Run 'gregale dlq list %s' to inspect failed messages.", status.DLQCount, status.Slug)
	}

	return 0
}

func cmdWorkersLogs(args []string) int {
	if len(args) == 0 {
		PrintUsage(osStderr, "usage: gregale workers logs [<app>] [--follow] [--since RFC3339] [--grep SUBSTR] [--level info|warn|error]", "workers")
		return 1
	}

	slug := ""
	var forwardArgs []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") && slug == "" {
			slug = arg
		} else {
			forwardArgs = append(forwardArgs, arg)
		}
	}

	var resolveErr error
	slug, resolveErr = resolveAppFlagOrContext(slug)
	if resolveErr != nil {
		if errors.Is(resolveErr, errProjectContextNotFound) {
			PrintUsage(osStderr, "usage: gregale workers logs <app> [--follow] (or run inside a linked project directory)", "workers")
			return 1
		}
		return printErr("Could not resolve app slug", resolveErr)
	}

	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}

	app, err := client.GetApp(context.Background(), slug)
	if err != nil {
		return printErr("Could not get app details", err)
	}
	if !isWorkerApp(app) {
		return printErr("Invalid app mode", fmt.Errorf("app %q has execution_mode=%q (not a worker). Use 'gregale logs %s' directly", slug, app.Manifest.EffectiveExecutionMode(), slug))
	}

	// Delegate to cmdLogs
	logArgs := append([]string{slug}, forwardArgs...)
	return cmdLogs(logArgs)
}

func cmdWorkersScale(args []string) int {
	usage := "usage: gregale workers scale <app> [--min N] [--max N] [--target N] [--metric METRIC] [--drain-timeout DURATION] [--stop-signal SIG]"
	fs := newFlagSet("workers scale", flag.ContinueOnError)
	minFlag := fs.Int("min", -1, "min worker replicas (0 = scale to zero)")
	maxFlag := fs.Int("max", -1, "max worker replicas")
	targetFlag := fs.Float64("target", -1, "target backlog per worker")
	metricFlag := fs.String("metric", "", "autoscaling metric (queue_lag | queue_depth)")
	drainFlag := fs.String("drain-timeout", "", "graceful drain duration before kill (e.g. 90s, 2m)")
	stopSignalFlag := fs.String("stop-signal", "", "stop signal sent during scale-down (e.g. SIGTERM, SIGINT)")

	flags, pos := splitArgsForFlags(args)
	if err := fs.Parse(flags); err != nil {
		PrintUsage(osStderr, usage, "workers")
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if len(pos) != 1 {
		PrintUsage(osStderr, usage, "workers")
		return 1
	}

	slug := pos[0]

	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}

	ctx := context.Background()
	app, err := client.GetApp(ctx, slug)
	if err != nil {
		return printErr("Could not get app details", err)
	}
	if !isWorkerApp(app) {
		return printErr("Invalid app mode", fmt.Errorf("app %q has execution_mode=%q (not a worker). Use 'gregale app %s scale' for HTTP apps", slug, app.Manifest.EffectiveExecutionMode(), slug))
	}

	var req api.UpdateAppRequest
	scalingChanged := false
	existingScaling := app.Manifest.WorkerReplicas
	var newScaling api.WorkerScaling
	if existingScaling != nil {
		newScaling = *existingScaling
	} else {
		newScaling = api.WorkerScaling{Min: 0, Max: 10, Metric: "queue_lag", Target: 100}
	}

	if *minFlag >= 0 {
		newScaling.Min = *minFlag
		scalingChanged = true
	}
	if *maxFlag >= 0 {
		newScaling.Max = *maxFlag
		scalingChanged = true
	}
	if *targetFlag >= 0 {
		newScaling.Target = *targetFlag
		scalingChanged = true
	}
	if *metricFlag != "" {
		if *metricFlag != "queue_lag" && *metricFlag != "queue_depth" {
			return printErr("Invalid metric", fmt.Errorf("metric %q must be one of {queue_lag, queue_depth}", *metricFlag))
		}
		newScaling.Metric = *metricFlag
		scalingChanged = true
	}

	if newScaling.Max < newScaling.Min {
		return printErr("Invalid scaling bounds", fmt.Errorf("max replicas (%d) cannot be less than min replicas (%d)", newScaling.Max, newScaling.Min))
	}

	if scalingChanged {
		req.WorkerReplicas = &newScaling
	}

	if *drainFlag != "" {
		var drainSeconds int
		if d, dErr := time.ParseDuration(*drainFlag); dErr == nil {
			drainSeconds = int(d.Seconds())
		} else {
			var parseErr error
			drainSeconds, parseErr = strconv.Atoi(*drainFlag)
			if parseErr != nil || drainSeconds < 0 {
				return printErr("Invalid --drain-timeout", fmt.Errorf("must be a valid duration (e.g. 90s, 2m) or non-negative seconds; got %q", *drainFlag))
			}
		}
		req.StopGracePeriodS = &drainSeconds
	}

	if *stopSignalFlag != "" {
		sig := strings.ToUpper(strings.TrimSpace(*stopSignalFlag))
		switch sig {
		case "SIGTERM", "SIGINT", "SIGQUIT", "SIGHUP", "SIGUSR1", "SIGUSR2":
			req.StopSignal = &sig
		default:
			return printErr("Invalid --stop-signal", fmt.Errorf("unsupported stop signal %q (expected: SIGTERM, SIGINT, SIGQUIT, SIGHUP, SIGUSR1, SIGUSR2)", sig))
		}
	}

	updatedApp, err := client.UpdateApp(ctx, slug, req)
	if err != nil {
		return printErr("Could not update worker scaling", err)
	}

	if jsonOutput {
		return jsonOut(writeJSON(updatedApp))
	}

	PrintOK(osStdout, "Worker scaling updated for %s", slug)
	if updatedApp.Manifest.WorkerReplicas != nil {
		_, _ = fmt.Fprintf(osStdout, "  Replicas: min=%d, max=%d\n", updatedApp.Manifest.WorkerReplicas.Min, updatedApp.Manifest.WorkerReplicas.Max)
		_, _ = fmt.Fprintf(osStdout, "  Scaling:  target=%.0f, metric=%s\n", updatedApp.Manifest.WorkerReplicas.Target, updatedApp.Manifest.WorkerReplicas.Metric)
	}
	if updatedApp.Manifest.StopGracePeriod > 0 {
		_, _ = fmt.Fprintf(osStdout, "  Drain:    %ds timeout\n", int(updatedApp.Manifest.StopGracePeriod.Seconds()))
	}
	if updatedApp.Manifest.StopSignal != "" {
		_, _ = fmt.Fprintf(osStdout, "  Signal:   %s\n", updatedApp.Manifest.StopSignal)
	}

	return 0
}
