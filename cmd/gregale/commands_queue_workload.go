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
	return setupQueueWorkloadWithRetry(ctx, client, slug, queueName, targetDepth, maxConcurrency, force, nil)
}

func setupQueueWorkloadWithRetry(ctx context.Context, client queueWorkloadClient, slug, queueName string, targetDepth float64, maxConcurrency int, force bool, retryPolicy *api.RetryPolicyDTO) (api.QueueWorkloadProfileResponse, error) {
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
		QueueName: queueName, TargetDepth: targetDepth, MaxConcurrency: maxConcurrency, RetryPolicy: retryPolicy, Force: force,
	})
	if err != nil {
		return api.QueueWorkloadProfileResponse{}, fmt.Errorf("configure queue workload: %w", err)
	}
	return result, nil
}

func validateQueueRetryPolicy(policy *api.RetryPolicyDTO) error {
	if policy == nil {
		return nil
	}
	if policy.MaxAttempts < 0 || policy.MaxAttempts > 25 {
		return fmt.Errorf("retry max attempts must be between 0 and 25")
	}
	if policy.BaseSeconds < 0 || policy.BaseSeconds > 3600 {
		return fmt.Errorf("retry base seconds must be between 0 and 3600")
	}
	if policy.MaxSeconds < 0 || policy.MaxSeconds > 86400 {
		return fmt.Errorf("retry max seconds must be between 0 and 86400")
	}
	if policy.JitterSeconds < 0 || policy.JitterSeconds > 1 {
		return fmt.Errorf("retry jitter seconds must be between 0 and 1")
	}
	if policy.MaxSeconds > 0 && policy.BaseSeconds > policy.MaxSeconds {
		return fmt.Errorf("retry base seconds must not exceed max seconds")
	}
	return nil
}

func retryPolicyFromQueueSetupFlags(fs *flag.FlagSet, maxAttempts *int, baseSeconds, maxSeconds, jitterSeconds *float64) (*api.RetryPolicyDTO, error) {
	set := false
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "max-attempts", "retry-base-seconds", "retry-max-seconds", "retry-jitter-seconds":
			set = true
		}
	})
	if !set {
		return nil, nil
	}
	policy := &api.RetryPolicyDTO{
		MaxAttempts: *maxAttempts, BaseSeconds: *baseSeconds,
		MaxSeconds: *maxSeconds, JitterSeconds: *jitterSeconds,
	}
	if err := validateQueueRetryPolicy(policy); err != nil {
		return nil, err
	}
	return policy, nil
}

func cmdQueueSetup(args []string) int {
	fs := newFlagSet("queue setup", flag.ContinueOnError)
	queueName := fs.String("queue-name", defaultQueueWorkloadQueueName, "logical queue name")
	targetDepth := fs.Float64("target-depth", defaultQueueWorkloadTargetDepth, "messages per worker before scaling out")
	maxConcurrency := fs.Int("max-concurrency", defaultQueueWorkloadConcurrency, "maximum concurrent deliveries per worker")
	maxAttempts := fs.Int("max-attempts", 0, "maximum delivery attempts (0 uses the plan default)")
	retryBaseSeconds := fs.Float64("retry-base-seconds", 0, "base retry delay in seconds")
	retryMaxSeconds := fs.Float64("retry-max-seconds", 0, "maximum retry delay in seconds")
	retryJitterSeconds := fs.Float64("retry-jitter-seconds", 0, "retry jitter in seconds (0..1)")
	force := fs.Bool("force", false, "replace an existing default binding that points at another queue")
	flags, pos := splitArgsForFlags(args)
	if err := fs.Parse(flags); err != nil || len(pos) != 1 {
		PrintUsage(os.Stderr, "usage: gregale queue setup <slug> [--queue-name QUEUE] [--target-depth N] [--max-concurrency N] [--max-attempts N] [--retry-base-seconds N] [--retry-max-seconds N] [--retry-jitter-seconds N] [--force]", "queue")
		return 1
	}
	retryPolicy, err := retryPolicyFromQueueSetupFlags(fs, maxAttempts, retryBaseSeconds, retryMaxSeconds, retryJitterSeconds)
	if err != nil {
		return printErr("Invalid retry policy", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	result, err := setupQueueWorkloadWithRetry(context.Background(), client, pos[0], *queueName, *targetDepth, *maxConcurrency, *force, retryPolicy)
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
