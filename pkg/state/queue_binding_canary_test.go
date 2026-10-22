// adr:1352
package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
	"time"
)

func TestQueueCanaryLifecycleAndIsolation(t *testing.T) {
	m := state.NewMemStore()
	ctx := context.Background()
	account, err := m.CreateAccount(ctx, "canary@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := m.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "canary", Type: state.AppTypeFunction, WorkloadClass: state.WorkloadClassHTTP, Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	env, _ := json.Marshal(map[string]string{api.QueueCanaryCapabilityEnv: `["orders"]`})
	dep, err := m.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:canary", OverrideEnv: env})
	if err != nil {
		t.Fatal(err)
	}
	if err = m.SetDeploymentRootfs(ctx, dep.ID, "/test/rootfs", "test/rootfs", 1); err != nil {
		t.Fatal(err)
	}
	if err = m.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	created, err := m.CreateQueueBindingWithConsumer(ctx, state.QueueBinding{AccountID: account.ID, AppID: app.ID, Name: "orders", QueueName: "orders", Mode: "push", WorkloadClass: state.WorkloadClassHTTP, Enabled: true, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	req := api.CreateQueueBindingCanaryRequest{DeploymentID: dep.ID, Binding: "orders", TTLSeconds: 60, PublishConsent: true}
	denied := req
	denied.PublishConsent = false
	if _, err = m.CreateQueueBindingCanary(ctx, account.ID, app.ID, denied); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("consent: %v", err)
	}
	r, err := m.CreateQueueBindingCanary(ctx, account.ID, app.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.CreateQueueBindingCanary(ctx, account.ID, app.ID, req); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("duplicate: %v", err)
	}
	if _, err = m.GetQueueBindingCanary(ctx, "foreign", app.ID, r.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("isolation: %v", err)
	}
	inv := (*m.Invocations())[r.ID]
	if inv.QueueBindingID != created.Binding.ID || inv.DeadlineAt == nil || inv.RetryPolicy().MaxAttempts != 1 {
		t.Fatalf("admission: %+v", inv)
	}
	now := time.Now()
	inv.State = state.InvocationDispatching
	inv.Attempts = 1
	inv.ReceivedAt = &now
	(*m.Invocations())[r.ID] = inv
	got, err := m.GetQueueBindingCanary(ctx, account.ID, app.ID, r.ID)
	if err != nil || got.Status != "dispatching" || got.DeliveredAt != nil {
		t.Fatalf("claim is not delivery: %+v %v", got, err)
	}
	if err = state.ValidateQueueCanaryDispatch(ctx, m, app, inv, true); err != nil {
		t.Fatal(err)
	}
	inv.State = state.InvocationCompleted
	inv.CompletedAt = &now
	(*m.Invocations())[r.ID] = inv
	got, _ = m.GetQueueBindingCanary(ctx, account.ID, app.ID, r.ID)
	if got.Status != "failed" {
		t.Fatalf("unconfirmed completion passed: %+v", got)
	}
	record := (*m.QueueBindingCanaries())[r.ID]
	record.CompletedDeploymentID = dep.ID
	record.CompletedAttempt = 1
	(*m.QueueBindingCanaries())[r.ID] = record
	got, _ = m.GetQueueBindingCanary(ctx, account.ID, app.ID, r.ID)
	if got.Status != "succeeded" {
		t.Fatalf("completion: %+v", got)
	}
	enabled := false
	if _, err = m.UpdateQueueBindingWithConsumer(ctx, account.ID, app.ID, created.Binding.ID, state.UpdateQueueBindingParams{Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	got, _ = m.GetQueueBindingCanary(ctx, account.ID, app.ID, r.ID)
	if got.Status != "blocked" {
		t.Fatalf("changed consumer passed: %+v", got)
	}
	got, err = m.CancelQueueBindingCanary(ctx, account.ID, app.ID, r.ID)
	if err != nil || got.Status != "cancelled" {
		t.Fatalf("cancel: %+v %v", got, err)
	}
	enabled = true
	if _, err = m.UpdateQueueBindingWithConsumer(ctx, account.ID, app.ID, created.Binding.ID, state.UpdateQueueBindingParams{Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < api.MustLimitsFor(api.PlanPro).MaxQueueDepth; i++ {
		if _, err = m.EnqueueInvocation(ctx, state.Invocation{AccountID: account.ID, AppID: app.ID, Source: state.InvocationQueue, QueueName: "orders", State: state.InvocationPending, DueAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = m.CreateQueueBindingCanary(ctx, account.ID, app.ID, req); !errors.Is(err, state.ErrQuotaExceeded) {
		t.Fatalf("canary bypassed queue depth: %v", err)
	}

}
func TestQueueCanaryCompletionFences(t *testing.T) {
	now := time.Now()
	s := state.QueueCanarySnapshot{DeploymentID: "candidate", Name: "orders"}
	base := state.QueueCanaryRecord{Snapshot: s, CompletedDeploymentID: "candidate", CompletedAttempt: 1, Report: api.QueueBindingCanary{DeploymentID: "candidate", ExpiresAt: now.Add(time.Minute)}}
	inv := state.Invocation{State: state.InvocationCompleted, Attempts: 1, ReceivedAt: &now, CompletedAt: &now}
	for _, tc := range []struct {
		name   string
		mutate func(*state.QueueCanaryRecord, *state.Invocation)
	}{
		{"wrong deployment", func(r *state.QueueCanaryRecord, _ *state.Invocation) { r.CompletedDeploymentID = "other" }},
		{"stale attempt", func(r *state.QueueCanaryRecord, _ *state.Invocation) { r.CompletedAttempt = 2 }},
		{"replay", func(_ *state.QueueCanaryRecord, i *state.Invocation) { i.ReplayGeneration = 1 }},
		{"expired", func(r *state.QueueCanaryRecord, _ *state.Invocation) { r.Report.ExpiresAt = now.Add(-time.Second) }},
		{"failed first attempt", func(_ *state.QueueCanaryRecord, i *state.Invocation) { i.State = state.InvocationPending }},
		{"handler failed", func(_ *state.QueueCanaryRecord, i *state.Invocation) { i.State = state.InvocationFailed }},
		{"cancelled", func(r *state.QueueCanaryRecord, _ *state.Invocation) { r.Cancelled = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, i := base, inv
			tc.mutate(&r, &i)
			if got := state.QueueCanaryStatus(r, i, &s, now); got.Status == "succeeded" {
				t.Fatalf("false pass: %+v", got)
			}
		})
	}
}
