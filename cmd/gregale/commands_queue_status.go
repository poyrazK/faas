package main

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// queueStatusClient is the read-only surface needed by queue status. Keeping
// the command on existing app, queue, and binding endpoints means the CLI can
// provide a useful aggregate without introducing another control-plane API.
type queueStatusClient interface {
	GetApp(context.Context, string) (api.AppResponse, error)
	QueueState(context.Context, string) (api.QueueStateResponse, error)
	ListQueueBindings(context.Context, string) ([]api.QueueBindingResponse, error)
	GetQueueBindingStatus(context.Context, string, string) (api.QueueBindingStatusResponse, error)
}

// queueStatusReport is the stable JSON shape emitted by `queue status`. The
// nested values deliberately preserve the existing API contracts so scripts
// can move from the CLI to the SDK without translating fields.
type queueStatusReport struct {
	AppSlug       string                 `json:"app_slug"`
	Health        string                 `json:"health"`
	Queue         api.QueueStateResponse `json:"queue"`
	ScalingPolicy *api.ScalingPolicy     `json:"scaling_policy,omitempty"`
	// MinInstances and MaxConcurrency are the app's own bounds, which a
	// scaling policy's zero min/max fall back to.
	MinInstances   int                              `json:"min_instances"`
	MaxConcurrency int                              `json:"max_concurrency"`
	Bindings       []api.QueueBindingStatusResponse `json:"bindings"`
}

func collectQueueStatus(ctx context.Context, client queueStatusClient, slug string) (queueStatusReport, error) {
	app, err := client.GetApp(ctx, slug)
	if err != nil {
		return queueStatusReport{}, fmt.Errorf("load app: %w", err)
	}
	queue, err := client.QueueState(ctx, slug)
	if err != nil {
		return queueStatusReport{}, fmt.Errorf("load queue state: %w", err)
	}
	bindings, err := client.ListQueueBindings(ctx, slug)
	if err != nil {
		return queueStatusReport{}, fmt.Errorf("list queue bindings: %w", err)
	}

	statuses := make([]api.QueueBindingStatusResponse, 0, len(bindings))
	for _, binding := range bindings {
		status, err := client.GetQueueBindingStatus(ctx, slug, binding.ID)
		if err != nil {
			return queueStatusReport{}, fmt.Errorf("load queue binding %q status: %w", binding.Name, err)
		}
		statuses = append(statuses, status)
	}
	return queueStatusReport{
		AppSlug:        slug,
		Health:         queueStatusHealth(statuses),
		Queue:          queue,
		ScalingPolicy:  app.ScalingPolicy,
		MinInstances:   app.MinInstances,
		MaxConcurrency: app.MaxConcurrency,
		Bindings:       statuses,
	}, nil
}

func queueStatusHealth(statuses []api.QueueBindingStatusResponse) string {
	if len(statuses) == 0 {
		return "not_configured"
	}
	worst := "healthy"
	worstRank := 0
	for _, status := range statuses {
		health := status.ConsumerLiveness
		if health == "" {
			health = "unknown"
		}
		rank := queueStatusHealthRank(health)
		if rank > worstRank {
			worst, worstRank = health, rank
		}
	}
	return worst
}

func queueStatusHealthRank(health string) int {
	switch health {
	case "healthy":
		return 0
	case "external":
		return 1
	case "not_observed":
		return 2
	case "unknown":
		return 3
	case "degraded":
		return 4
	case "stale":
		return 5
	default:
		return 3
	}
}

func cmdQueueStatus(args []string) int {
	fs := newFlagSet("queue status", flag.ContinueOnError)
	appFlag := fs.String("app", "", appSlugFlagUsage)
	flags, pos := splitArgsForFlags(args)
	if err := fs.Parse(flags); err != nil {
		PrintUsage(osStderr, "usage: gregale queue status <slug>", "queue")
		return 1
	}
	pos, mergeErr := mergeAppFlag(pos, strings.TrimSpace(*appFlag), 1)
	if mergeErr != nil {
		return printErr("Invalid arguments", mergeErr)
	}
	if len(pos) != 1 {
		PrintUsage(osStderr, "usage: gregale queue status <slug>", "queue")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	report, err := collectQueueStatus(context.Background(), client, pos[0])
	if err != nil {
		return printErr("Queue status failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(report))
	}
	renderQueueStatus(report)
	return 0
}

func renderQueueStatus(report queueStatusReport) {
	_, _ = fmt.Fprintf(osStdout, "app:        %s\n", report.AppSlug)
	_, _ = fmt.Fprintf(osStdout, "health:     %s\n", report.Health)
	_, _ = fmt.Fprintf(osStdout, "queue:      depth=%d in_flight=%d cap=%d\n", report.Queue.Depth, report.Queue.InFlight, report.Queue.PlanCap)
	if report.Queue.OldestPendingAgeSeconds != nil {
		_, _ = fmt.Fprintf(osStdout, "oldest_age: %ds\n", *report.Queue.OldestPendingAgeSeconds)
	}
	if report.ScalingPolicy != nil || report.MaxConcurrency > 0 {
		_, _ = fmt.Fprintf(osStdout, "scaling:    %s", queueScalingBounds(report))
		if report.ScalingPolicy != nil && report.ScalingPolicy.Target != nil {
			target := report.ScalingPolicy.Target
			_, _ = fmt.Fprintf(osStdout, " target=%s:%g", target.Metric, target.Value)
		}
		_, _ = fmt.Fprintln(osStdout)
	}
	if len(report.Bindings) == 0 {
		_, _ = fmt.Fprintln(osStdout, "bindings:    none")
		return
	}
	for _, binding := range report.Bindings {
		_, _ = fmt.Fprintf(osStdout, "binding:    %s queue=%s mode=%s class=%s environment=%s enabled=%t consumer=%s liveness=%s", binding.Name, binding.QueueName, binding.Mode, binding.WorkloadClass, queueEnvironmentLabel(binding.Environment), binding.Enabled, binding.ConsumerState, binding.ConsumerLiveness)
		if binding.LagMessages != nil {
			_, _ = fmt.Fprintf(osStdout, " lag=%d", *binding.LagMessages)
		}
		if binding.LagAgeSeconds != nil {
			_, _ = fmt.Fprintf(osStdout, " lag_age=%.1fs", *binding.LagAgeSeconds)
		}
		if binding.DeadLetter > 0 {
			_, _ = fmt.Fprintf(osStdout, " dead_letter=%d", binding.DeadLetter)
		}
		_, _ = fmt.Fprintln(osStdout)
		if binding.LastError != "" {
			_, _ = fmt.Fprintf(osStdout, "  last_error: %s\n", binding.LastError)
		}
	}
}

func queueEnvironmentLabel(environment string) string {
	if environment == "" {
		return "shared"
	}
	return environment
}

// queueScalingBounds renders the bounds the app scales between. A scaling
// policy's 0 min or max means "not set"; printing it raw read "min=0 max=0"
// for a consumer that can run max_concurrency instances (H10-75).
func queueScalingBounds(report queueStatusReport) string {
	minInstances, maxInstances := report.MinInstances, 0
	if p := report.ScalingPolicy; p != nil {
		if p.MinInstances > 0 {
			minInstances = p.MinInstances
		}
		maxInstances = p.MaxInstances
	}
	if maxInstances > 0 {
		return fmt.Sprintf("min=%d max=%d", minInstances, maxInstances)
	}
	return fmt.Sprintf("min=%d max=%d (max_concurrency)", minInstances, report.MaxConcurrency)
}
