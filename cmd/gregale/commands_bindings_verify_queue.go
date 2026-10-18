// adr:1352
package main

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"os"
	"os/signal"
	"time"
)

type queueCanaryConsentKey struct{}
type queueCanaryScopeKey struct{}

func runQueueBindingCanary(ctx context.Context, client *api.Client, app, binding, ref string, consent bool, interval, timeout time.Duration) int {
	id, err := resolveBindingDeployment(ctx, client, app, ref)
	if err != nil {
		return printErr("Could not select deployment", err)
	}
	interrupted, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	bounded, cancel := context.WithTimeout(interrupted, timeout)
	defer cancel()
	r, err := executeQueueBindingCanary(bounded, client, app, binding, id, consent, interval, "")
	if err != nil {
		return printErr("Queue canary did not pass", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(r))
	}
	PrintOK(osStdout, "Queue binding %s completed on deployment %s", binding, r.DeploymentID)
	return 0
}
func executeQueueBindingCanary(ctx context.Context, client *api.Client, app, binding, deployment string, consent bool, interval time.Duration, pending string) (api.QueueBindingCanary, error) {
	var r api.QueueBindingCanary
	var err error
	selected, durable := ctx.Value(bindingVerificationRunStepKey{}).(bindingVerificationRunStepContext)
	if durable && selected.Run.Steps[selected.Index].Plan.Action == "queue-canary" {
		step, admitErr := admitVerificationRunStep(ctx, client, app, "queue")
		if admitErr != nil {
			return r, admitErr
		}
		r, err = client.GetQueueBindingCanary(ctx, app, step.ResourceID)
		if err == nil && r.ID != step.ResourceID {
			return r, fmt.Errorf("run canary receipt identity changed")
		}
	} else if pending != "" {
		r, err = client.GetQueueBindingCanary(ctx, app, pending)
	} else {
		if !consent {
			return r, fmt.Errorf("review --dry-run, then supply --queue-publish to consent to one safe message per planned queue binding")
		}
		r, err = client.CreateQueueBindingCanary(ctx, app, api.CreateQueueBindingCanaryRequest{DeploymentID: deployment, Binding: binding, TTLSeconds: api.QueueCanaryMaxTTLSeconds, PublishConsent: true})
	}
	if err != nil {
		return r, err
	}
	id, parseErr := uuid.Parse(r.ID)
	expectedScope, scopeSelected := ctx.Value(queueCanaryScopeKey{}).(string)
	if parseErr != nil || id == uuid.Nil || !sameBindingDeployment(r.DeploymentID, deployment) || r.Binding != binding || r.ExpiresAt.IsZero() || r.PublishedAt.IsZero() || !r.ExpiresAt.After(r.PublishedAt) || r.ExpiresAt.Sub(r.PublishedAt) > time.Duration(api.QueueCanaryMaxTTLSeconds)*time.Second || api.ValidateScope(r.Scope) != nil || scopeSelected && r.Scope != expectedScope {
		return r, fmt.Errorf("server did not confirm the exact canary identity")
	}
	completed := false
	defer func() {
		if bindingProbeAutoCancel(ctx) && !completed && pending == "" {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), bindingProbeCancelTimeout)
			defer cancel()
			if _, err := client.CancelQueueBindingCanary(cleanup, app, r.ID); err != nil {
				PrintWarn(osStderr, "Could not cancel queue canary %s: %v", r.ID, err)
			}
		}
	}()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		switch r.Status {
		case "succeeded":
			if r.CompletedAt == nil || r.DeliveredAt == nil || r.DeliveredAt.Before(r.PublishedAt) || r.CompletedAt.Before(*r.DeliveredAt) || r.CompletedAt.After(time.Now()) || !r.ExpiresAt.After(time.Now()) {
				return r, fmt.Errorf("server did not confirm handler completion")
			}
			completed = true
			return r, nil
		case "published", "dispatching":
		default:
			return r, fmt.Errorf("queue canary %s: %s", r.Status, r.Reason)
		}
		select {
		case <-ctx.Done():
			return r, ctx.Err()
		case <-ticker.C:
		}
		next, err := client.GetQueueBindingCanary(ctx, app, r.ID)
		if err != nil {
			return r, err
		}
		if next.ID != r.ID || next.DeploymentID != r.DeploymentID || next.Binding != r.Binding || next.Scope != r.Scope || !next.PublishedAt.Equal(r.PublishedAt) || !next.ExpiresAt.Equal(r.ExpiresAt) {
			return r, fmt.Errorf("queue canary identity changed")
		}
		r = next
	}
}
