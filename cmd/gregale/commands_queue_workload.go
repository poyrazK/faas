package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	defaultQueueWorkloadQueueName   = "default"
	defaultQueueWorkloadTargetDepth = 10.0
	defaultQueueWorkloadConcurrency = 1
)

// queueWorkloadClient is the single server-side control-plane surface used by
// the simple queue profile. Keeping the CLI thin prevents it from having to
// recreate binding/scaling reconciliation and rollback rules.
type queueWorkloadClient interface {
	ConfigureQueueWorkload(context.Context, string, api.QueueWorkloadProfileRequest) (api.QueueWorkloadProfileResponse, error)
}

func setupQueueWorkload(ctx context.Context, client queueWorkloadClient, slug, queueName string, targetDepth float64, maxConcurrency int, force bool) (api.QueueWorkloadProfileResponse, error) {
	if queueName == "" {
		queueName = defaultQueueWorkloadQueueName
	}
	if targetDepth <= 0 {
		return api.QueueWorkloadProfileResponse{}, fmt.Errorf("target depth must be greater than zero")
	}
	if maxConcurrency < 1 {
		return api.QueueWorkloadProfileResponse{}, fmt.Errorf("max concurrency must be at least one")
	}
	result, err := client.ConfigureQueueWorkload(ctx, slug, api.QueueWorkloadProfileRequest{
		QueueName: queueName, TargetDepth: targetDepth, MaxConcurrency: maxConcurrency, Force: force,
	})
	if err != nil {
		return api.QueueWorkloadProfileResponse{}, fmt.Errorf("configure queue workload: %w", err)
	}
	return result, nil
}

func cmdQueueSetup(args []string) int {
	fs := newFlagSet("queue setup", flag.ContinueOnError)
	queueName := fs.String("queue-name", defaultQueueWorkloadQueueName, "logical queue name")
	targetDepth := fs.Float64("target-depth", defaultQueueWorkloadTargetDepth, "messages per worker before scaling out")
	maxConcurrency := fs.Int("max-concurrency", defaultQueueWorkloadConcurrency, "maximum concurrent deliveries per worker")
	force := fs.Bool("force", false, "replace an existing default binding that points at another queue")
	flags, pos := splitArgsForFlags(args)
	if err := fs.Parse(flags); err != nil || len(pos) != 1 {
		PrintUsage(os.Stderr, "usage: gregale queue setup <slug> [--queue-name QUEUE] [--target-depth N] [--max-concurrency N] [--force]", "queue")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	result, err := setupQueueWorkload(context.Background(), client, pos[0], *queueName, *targetDepth, *maxConcurrency, *force)
	if err != nil {
		return printErr("Queue workload setup failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	verb := "configured"
	if result.Created {
		verb = "created"
	}
	PrintOK(osStdout, "Queue workload %s: %s -> %s (%s push, queue_depth target %g).", verb, result.Binding.Name, result.Binding.QueueName, result.Binding.WorkloadClass, result.ScalingPolicy.Target.Value)
	return 0
}
