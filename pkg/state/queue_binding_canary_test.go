// adr:1352
package state

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"testing"
	"time"
)

func TestQueueCanaryLifecycleAndIsolation(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	account, err := m.CreateAccount(ctx, "canary@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := m.CreateApp(ctx, App{AccountID: account.ID, Slug: "canary", Type: AppTypeFunction, WorkloadClass: WorkloadClassHTTP, Manifest: AppManifest{RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	env, _ := json.Marshal(map[string]string{api.QueueCanaryCapabilityEnv: `["orders"]`})
	dep, err := m.CreateDeployment(ctx, Deployment{AppID: app.ID, ImageDigest: "sha256:canary", OverrideEnv: env})
	if err != nil {
		t.Fatal(err)
	}
	if err = m.SetDeploymentRootfs(ctx, dep.ID, "/test/rootfs", "test/rootfs", 1); err != nil {
		t.Fatal(err)
	}
	if err = m.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	created, err := m.CreateQueueBindingWithConsumer(ctx, QueueBinding{AccountID: account.ID, AppID: app.ID, Name: "orders", QueueName: "orders", Mode: "push", WorkloadClass: WorkloadClassHTTP, Enabled: true, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	req := api.CreateQueueBindingCanaryRequest{DeploymentID: dep.ID, Binding: "orders", TTLSeconds: 60, PublishConsent: true}
	denied := req
	denied.PublishConsent = false
	if _, err = m.CreateQueueBindingCanary(ctx, account.ID, app.ID, denied); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("consent: %v", err)
	}
	r, err := m.CreateQueueBindingCanary(ctx, account.ID, app.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.CreateQueueBindingCanary(ctx, account.ID, app.ID, req); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate: %v", err)
	}
	if _, err = m.GetQueueBindingCanary(ctx, "foreign", app.ID, r.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("isolation: %v", err)
	}
	inv := m.invocations[r.ID]
	if inv.QueueBindingID != created.Binding.ID || inv.DeadlineAt == nil || inv.RetryPolicy().MaxAttempts != 1 {
		t.Fatalf("admission: %+v", inv)
	}
	now := time.Now()
	inv.State = InvocationDispatching
	inv.Attempts = 1
	inv.ReceivedAt = &now
	m.invocations[r.ID] = inv
	got, err := m.GetQueueBindingCanary(ctx, account.ID, app.ID, r.ID)
	if err != nil || got.Status != "dispatching" || got.DeliveredAt != nil {
		t.Fatalf("claim is not delivery: %+v %v", got, err)
	}
	if err = validateQueueCanaryDispatch(ctx, m, app, inv, true); err != nil {
		t.Fatal(err)
	}
	inv.State = InvocationCompleted
	inv.CompletedAt = &now
	m.invocations[r.ID] = inv
	got, _ = m.GetQueueBindingCanary(ctx, account.ID, app.ID, r.ID)
	if got.Status != "failed" {
		t.Fatalf("unconfirmed completion passed: %+v", got)
	}
	record := m.queueBindingCanaries[r.ID]
	record.CompletedDeploymentID = dep.ID
	record.CompletedAttempt = 1
	m.queueBindingCanaries[r.ID] = record
	got, _ = m.GetQueueBindingCanary(ctx, account.ID, app.ID, r.ID)
	if got.Status != "succeeded" {
		t.Fatalf("completion: %+v", got)
	}
	enabled := false
	if _, err = m.UpdateQueueBindingWithConsumer(ctx, account.ID, app.ID, created.Binding.ID, UpdateQueueBindingParams{Enabled: &enabled}); err != nil {
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
	if _, err = m.UpdateQueueBindingWithConsumer(ctx, account.ID, app.ID, created.Binding.ID, UpdateQueueBindingParams{Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < api.MustLimitsFor(api.PlanPro).MaxQueueDepth; i++ {
		if _, err = m.EnqueueInvocation(ctx, Invocation{AccountID: account.ID, AppID: app.ID, Source: InvocationQueue, QueueName: "orders", State: InvocationPending, DueAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = m.CreateQueueBindingCanary(ctx, account.ID, app.ID, req); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("canary bypassed queue depth: %v", err)
	}

}
func TestQueueCanaryCompletionFences(t *testing.T) {
	now := time.Now()
	s := queueCanarySnapshot{DeploymentID: "candidate", Name: "orders"}
	base := queueCanaryRecord{Snapshot: s, CompletedDeploymentID: "candidate", CompletedAttempt: 1, Report: api.QueueBindingCanary{DeploymentID: "candidate", ExpiresAt: now.Add(time.Minute)}}
	inv := Invocation{State: InvocationCompleted, Attempts: 1, ReceivedAt: &now, CompletedAt: &now}
	for _, tc := range []struct {
		name   string
		mutate func(*queueCanaryRecord, *Invocation)
	}{
		{"wrong deployment", func(r *queueCanaryRecord, _ *Invocation) { r.CompletedDeploymentID = "other" }},
		{"stale attempt", func(r *queueCanaryRecord, _ *Invocation) { r.CompletedAttempt = 2 }},
		{"replay", func(_ *queueCanaryRecord, i *Invocation) { i.ReplayGeneration = 1 }},
		{"expired", func(r *queueCanaryRecord, _ *Invocation) { r.Report.ExpiresAt = now.Add(-time.Second) }},
		{"failed first attempt", func(_ *queueCanaryRecord, i *Invocation) { i.State = InvocationPending }},
		{"handler failed", func(_ *queueCanaryRecord, i *Invocation) { i.State = InvocationFailed }},
		{"cancelled", func(r *queueCanaryRecord, _ *Invocation) { r.Cancelled = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, i := base, inv
			tc.mutate(&r, &i)
			if got := queueCanaryStatus(r, i, &s, now); got.Status == "succeeded" {
				t.Fatalf("false pass: %+v", got)
			}
		})
	}
}
