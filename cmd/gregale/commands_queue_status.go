package main

import (
	"context"
	"flag"
	"fmt"

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
	AppSlug       string                           `json:"app_slug"`
	Health        string                           `json:"health"`
	Queue         api.QueueStateResponse           `json:"queue"`
	ScalingPolicy *api.ScalingPolicy               `json:"scaling_policy,omitempty"`
	Bindings      []api.QueueBindingStatusResponse `json:"bindings"`
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
		AppSlug:       slug,
		Health:        queueStatusHealth(statuses),
		Queue:         queue,
		ScalingPolicy: app.ScalingPolicy,
		Bindings:      statuses,
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
	flags, pos := splitArgsForFlags(args)
	if err := fs.Parse(flags); err != nil || len(pos) != 1 {
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
	if report.ScalingPolicy != nil {
		_, _ = fmt.Fprintf(osStdout, "scaling:    min=%d max=%d", report.ScalingPolicy.MinInstances, report.ScalingPolicy.MaxInstances)
		if target := report.ScalingPolicy.Target; target != nil {
			_, _ = fmt.Fprintf(osStdout, " target=%s:%g", target.Metric, target.Value)
		}
		_, _ = fmt.Fprintln(osStdout)
	}
	if len(report.Bindings) == 0 {
		_, _ = fmt.Fprintln(osStdout, "bindings:    none")
		return
	}
	for _, binding := range report.Bindings {
		_, _ = fmt.Fprintf(osStdout, "binding:    %s queue=%s mode=%s class=%s enabled=%t consumer=%s liveness=%s", binding.Name, binding.QueueName, binding.Mode, binding.WorkloadClass, binding.Enabled, binding.ConsumerState, binding.ConsumerLiveness)
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
