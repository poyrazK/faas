// adr:1353 — bounded parallel qualification preserves frozen plans and freshness.
package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const bindingVerificationConcurrencyDefault = api.BindingVerificationRunnerDefaultConcurrency
const bindingVerificationConcurrencyMax = api.BindingVerificationRunnerMaxConcurrency

type bindingVerificationBudgetKey struct{}
type bindingVerificationSnapshotKey struct{}
type bindingVerificationCancelKey struct{}
type bindingVerificationBudget struct{ slots chan struct{} }
type bindingVerificationOutcome struct {
	result *bindingPolicyProbeResult
	code   string
	err    error
}

func withBindingVerificationBudget(ctx context.Context, concurrency int) context.Context {
	if _, ok := ctx.Value(bindingVerificationBudgetKey{}).(*bindingVerificationBudget); ok {
		return ctx
	}
	return context.WithValue(ctx, bindingVerificationBudgetKey{}, &bindingVerificationBudget{slots: make(chan struct{}, concurrency)})
}

func bindingVerificationConcurrency(ctx context.Context) int {
	if b, ok := ctx.Value(bindingVerificationBudgetKey{}).(*bindingVerificationBudget); ok {
		return cap(b.slots)
	}
	return bindingVerificationConcurrencyDefault
}

func withBindingVerificationSlot(ctx context.Context, run func() error) error {
	b := ctx.Value(bindingVerificationBudgetKey{}).(*bindingVerificationBudget)
	select {
	case b.slots <- struct{}{}:
		defer func() { <-b.slots }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	err := run()
	if err != nil {
		if cancel, ok := ctx.Value(bindingVerificationCancelKey{}).(context.CancelFunc); ok {
			cancel()
		}
	}
	return err
}

// Each index has one writer. Join all work, including cancellation cleanup,
// before reading reports. A failing job prevents further admissions.
func runBindingVerificationJobs(ctx context.Context, count int, run func(context.Context, int) error) {
	bounded, cancel := context.WithCancel(ctx)
	defer cancel()
	bounded = context.WithValue(bounded, bindingVerificationCancelKey{}, cancel)
	var workers sync.WaitGroup
	for index := range count {
		workers.Go(func() {
			if err := bounded.Err(); err != nil {
				return
			}
			if err := run(bounded, index); err != nil {
				cancel()
			}
		})
	}
	workers.Wait()
}

func executeBindingPolicyStep(ctx context.Context, client *api.Client, snapshot *bindingPolicySnapshotClient, step bindingPolicyProbePlan, consent bool, interval, timeout time.Duration) (outcome bindingVerificationOutcome) {
	outcome = bindingVerificationOutcome{code: "probe_failed"}
	if err := snapshot.confirm(ctx); err != nil {
		outcome.err = err
		return outcome
	}
	var result bindingPolicyProbeResult
	if step.Action == "queue-canary" || step.Action == "wait-queue" {
		pending := ""
		if step.Action == "wait-queue" {
			for _, item := range snapshot.inventory.Bindings {
				if item.Type == api.BindingTypeQueue && item.Binding == step.Binding && item.QueueCanary != nil {
					pending = item.QueueCanary.ID
				}
			}
		}
		canary, err := executeQueueBindingCanary(ctx, client, snapshot.slug, step.Binding, snapshot.inventory.VerificationDeploymentID, consent, interval, pending)
		result = bindingPolicyProbeResult{Type: step.Type, Name: step.Name, Binding: step.Binding, Scope: step.Scope, Status: canary.Status, QueueReport: &canary}
		outcome.code, outcome.err = "queue_canary_failed", err
	} else {
		// Per-step command receipts avoid sharing mutable task maps between probes.
		probeClient := &bindingPolicyProbeClient{deploymentBindingProbeClient: &deploymentBindingProbeClient{Client: client, slug: snapshot.slug, inventory: snapshot.inventory}, snapshot: snapshot}
		defer func() {
			if bindingProbeAutoCancel(ctx) && outcome.err != nil && probeClient.ownedTaskID != "" && !probeClient.ownedTaskFinished && !probeClient.cancellationRequested {
				cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), bindingProbeCancelTimeout)
				defer cancel()
				if _, err := probeClient.CancelAppTask(cleanup, snapshot.slug, probeClient.ownedTaskID); err != nil {
					PrintWarn(osStderr, "could not cancel binding policy canary task %s: %v", probeClient.ownedTaskID, err)
				}
			}
		}()
		probe, title, code, err := executeBindingPolicyProbe(ctx, probeClient, step, interval, timeout)
		result, outcome.err = probe, err
		if code != 0 && err == nil {
			outcome.err = fmt.Errorf("%s: %s %s canary did not succeed", title, step.Type, step.Name)
		}
	}
	outcome.result = &result
	if outcome.err == nil {
		outcome.err = snapshot.confirm(ctx)
	}
	return outcome
}
