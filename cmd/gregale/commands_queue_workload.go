package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	defaultQueueWorkloadBindingName = "default"
	defaultQueueWorkloadQueueName   = "default"
	defaultQueueWorkloadTargetDepth = 10.0
	defaultQueueWorkloadConcurrency = 1
)

// queueWorkloadClient is the existing control-plane surface composed by the
// simple queue profile. Keeping this seam narrow makes the reconciliation
// logic testable without introducing a second queue configuration API.
type queueWorkloadClient interface {
	GetApp(context.Context, string) (api.AppResponse, error)
	UpdateApp(context.Context, string, api.UpdateAppRequest) (api.AppResponse, error)
	ListQueueBindings(context.Context, string) ([]api.QueueBindingResponse, error)
	CreateQueueBinding(context.Context, string, api.CreateQueueBindingRequest) (api.QueueBindingResponse, error)
	UpdateQueueBinding(context.Context, string, string, api.UpdateQueueBindingRequest) (api.QueueBindingResponse, error)
}

type queueWorkloadSetupResult struct {
	App           api.AppResponse          `json:"app"`
	Binding       api.QueueBindingResponse `json:"binding"`
	ScalingPolicy *api.ScalingPolicy       `json:"scaling_policy"`
	Created       bool                     `json:"created"`
}

var errQueueWorkloadBindingConflict = errors.New("default queue binding already points at another queue")

// setupQueueWorkload reconciles the opinionated worker profile used by
// `gregale queue setup`. It deliberately composes first-class bindings and
// app scaling rather than creating a parallel queue runtime or persistence
// model. Re-running it converges on the same binding and policy.
func setupQueueWorkload(ctx context.Context, client queueWorkloadClient, slug, queueName string, targetDepth float64, maxConcurrency int, force bool) (queueWorkloadSetupResult, error) {
	if queueName == "" {
		queueName = defaultQueueWorkloadQueueName
	}
	if targetDepth <= 0 {
		return queueWorkloadSetupResult{}, fmt.Errorf("target depth must be greater than zero")
	}
	if maxConcurrency < 1 {
		return queueWorkloadSetupResult{}, fmt.Errorf("max concurrency must be at least one")
	}

	app, err := client.GetApp(ctx, slug)
	if err != nil {
		return queueWorkloadSetupResult{}, fmt.Errorf("read app before queue setup: %w", err)
	}
	workloadClass := app.WorkloadClass
	if workloadClass != "worker" && workloadClass != "job" {
		return queueWorkloadSetupResult{}, fmt.Errorf("queue setup requires an app with workload_class worker or job; found %q", workloadClass)
	}
	bindings, err := client.ListQueueBindings(ctx, slug)
	if err != nil {
		return queueWorkloadSetupResult{}, fmt.Errorf("list queue bindings before queue setup: %w", err)
	}

	var existing *api.QueueBindingResponse
	for i := range bindings {
		if bindings[i].Name != defaultQueueWorkloadBindingName {
			continue
		}
		if existing != nil {
			return queueWorkloadSetupResult{}, fmt.Errorf("multiple %q queue bindings exist; reconcile them with `gregale queue bindings` first", defaultQueueWorkloadBindingName)
		}
		existing = &bindings[i]
	}
	if existing != nil && existing.QueueName != queueName && !force {
		return queueWorkloadSetupResult{}, fmt.Errorf("%w: %q (use --force to replace it)", errQueueWorkloadBindingConflict, existing.QueueName)
	}

	enabled := true
	mode := "push"
	var binding api.QueueBindingResponse
	created := false
	if existing == nil {
		binding, err = client.CreateQueueBinding(ctx, slug, api.CreateQueueBindingRequest{
			Name: defaultQueueWorkloadBindingName, QueueName: queueName,
			Mode: mode, WorkloadClass: workloadClass, Enabled: &enabled,
			MaxConcurrency: maxConcurrency,
		})
		created = true
	} else {
		binding, err = client.UpdateQueueBinding(ctx, slug, existing.ID, api.UpdateQueueBindingRequest{
			QueueName: &queueName, Mode: &mode, WorkloadClass: &workloadClass,
			Enabled: &enabled, MaxConcurrency: &maxConcurrency,
		})
	}
	if err != nil {
		return queueWorkloadSetupResult{}, fmt.Errorf("reconcile default queue binding: %w", err)
	}

	policy := queueWorkloadScalingPolicy(app, targetDepth)
	if !scalingPolicyEqual(app.ScalingPolicy, policy) || app.MinInstances != policy.MinInstances {
		updatedApp, updateErr := client.UpdateApp(ctx, slug, api.UpdateAppRequest{ScalingPolicy: policy})
		if updateErr != nil {
			return queueWorkloadSetupResult{}, fmt.Errorf("apply queue-depth scaling policy: %w", updateErr)
		}
		app = updatedApp
	}
	return queueWorkloadSetupResult{App: app, Binding: binding, ScalingPolicy: policy, Created: created}, nil
}

func queueWorkloadScalingPolicy(app api.AppResponse, targetDepth float64) *api.ScalingPolicy {
	policy := &api.ScalingPolicy{
		MinInstances:      0,
		ScaleOutCooldownS: 5,
		ScaleInCooldownS:  60,
		Target:            &api.ScalingTarget{Metric: "queue_depth", Value: targetDepth},
	}
	if app.ScalingPolicy != nil {
		copyPolicy := *app.ScalingPolicy
		if app.ScalingPolicy.Target != nil {
			target := *app.ScalingPolicy.Target
			copyPolicy.Target = &target
		}
		policy = &copyPolicy
		policy.MinInstances = 0
		if policy.ScaleOutCooldownS == 0 {
			policy.ScaleOutCooldownS = 5
		}
		if policy.ScaleInCooldownS == 0 {
			policy.ScaleInCooldownS = 60
		}
		policy.Target = &api.ScalingTarget{Metric: "queue_depth", Value: targetDepth}
	}
	return policy
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
