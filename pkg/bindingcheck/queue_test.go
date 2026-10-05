package bindingcheck

import (
	"slices"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestEvaluateQueueReadinessCannotBeWaivedOrHiddenByPassedProbes(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		mutate     func(*api.AppBindingInventoryItem, time.Time)
	}{
		{"missing", "queue_consumer_missing", func(b *api.AppBindingInventoryItem, _ time.Time) { b.ConsumerState = "not_configured" }},
		{"paused", "queue_consumer_paused", func(b *api.AppBindingInventoryItem, _ time.Time) { b.ConsumerState = "paused" }},
		{"unknown state", "queue_consumer_unknown", func(b *api.AppBindingInventoryItem, _ time.Time) { b.ConsumerState = "unknown" }},
		{"missing mode", "queue_consumer_unknown", func(b *api.AppBindingInventoryItem, _ time.Time) { b.Access = "" }},
		{"missing timestamp", "queue_consumer_unobserved", func(b *api.AppBindingInventoryItem, _ time.Time) { b.ObservedAt = nil }},
		{"zero timestamp", "queue_consumer_unobserved", func(b *api.AppBindingInventoryItem, _ time.Time) { b.ObservedAt = new(time.Time) }},
		{"future timestamp", "queue_consumer_time_future", func(b *api.AppBindingInventoryItem, now time.Time) {
			at := now.Add(time.Nanosecond)
			b.ObservedAt = &at
		}},
		{"healthy but expired", "queue_consumer_stale", func(b *api.AppBindingInventoryItem, now time.Time) {
			at := now.Add(-api.QueueConsumerMaxPollAge - time.Nanosecond)
			b.ObservedAt = &at
		}},
		{"reported stale", "queue_consumer_stale", func(b *api.AppBindingInventoryItem, _ time.Time) { b.ConsumerLiveness = "stale" }},
		{"degraded", "queue_consumer_degraded", func(b *api.AppBindingInventoryItem, _ time.Time) { b.ConsumerLiveness = "degraded" }},
		{"unobserved", "queue_consumer_unobserved", func(b *api.AppBindingInventoryItem, _ time.Time) { b.ConsumerLiveness = "not_observed" }},
		{"unknown liveness", "queue_consumer_unknown", func(b *api.AppBindingInventoryItem, _ time.Time) { b.ConsumerLiveness = "unknown" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inventory, policy, now := fixture()
			inventory.Bindings = append(inventory.Bindings, passedQueueFixtureOutbound(inventory, now))
			policy.AllowUnsupported = true
			polled := now.Add(-time.Second)
			item := api.AppBindingInventoryItem{Type: api.BindingTypeQueue, Name: "orders", Binding: "worker", Scope: "app", State: "enabled",
				Access: "push", ConsumerState: "active", ConsumerLiveness: "healthy", ObservedAt: &polled}
			tc.mutate(&item, now)
			inventory.Bindings = append(inventory.Bindings, item)
			report, err := Evaluate(inventory, policy, now)
			if err != nil || report.Passed || report.Coverage != "partial" || len(report.Blockers) != 1 || !slices.Contains(codes(report.Blockers), tc.code) || !slices.Contains(codes(report.Warnings), "verification_unsupported") {
				t.Fatalf("queue failure hidden by passed service/DB/S3/outbound probes or waiver: %+v %v", report, err)
			}
			for _, binding := range report.Bindings {
				if binding.Type == api.BindingTypeQueue && (binding.Status != "blocked" || binding.Reason != tc.code) {
					t.Fatalf("queue blocker missing from binding result: %+v", binding)
				}
			}
		})
	}
}

func passedQueueFixtureOutbound(inventory api.AppBindingInventory, now time.Time) api.AppBindingInventoryItem {
	credential, checked := true, now.Add(-time.Minute)
	item := api.AppBindingInventoryItem{Type: api.BindingTypeOutbound, Name: "provider", Scope: "app", State: "enabled",
		CredentialConfigured: &credential, OutboundProbe: &api.OutboundBindingProbePolicy{Method: "GET", Path: "/health", ExpectedStatus: 200},
		VerificationStatus: "passed", Verification: &api.BindingVerification{Result: "passed", Source: "task_guest", DeploymentID: inventory.VerificationDeploymentID, Scope: inventory.VerificationScope, CheckedAt: &checked}}
	for _, name := range []string{"configuration", "identity", "gateway", "response"} {
		item.Verification.Checks = append(item.Verification.Checks, api.BindingVerificationCheck{Name: name, Status: "passed"})
	}
	return item
}

func TestEvaluateQueuePollBoundaryAndCoverage(t *testing.T) {
	inventory, policy, now := fixture()
	policy.AllowUnsupported = true
	polled := now.Add(-api.QueueConsumerMaxPollAge)
	inventory.Bindings = []api.AppBindingInventoryItem{{Type: api.BindingTypeQueue, Name: "orders", Binding: "worker", Scope: "app", State: "enabled",
		Access: "push", ConsumerState: "active", ConsumerLiveness: "healthy", ObservedAt: &polled}}
	report, err := Evaluate(inventory, policy, now)
	if err != nil || !report.Passed || report.Coverage != "partial" || report.Bindings[0].Status != "unsupported" || report.Bindings[0].CheckedAt != nil {
		t.Fatalf("healthy idle poll claimed connectivity proof: %+v %v", report, err)
	}
	// Connectivity proof age cannot extend the independent consumer deadline.
	policy.MaxVerificationAge = time.Hour
	report, err = Evaluate(inventory, policy, now.Add(time.Nanosecond))
	if err != nil || report.Passed || !slices.Contains(codes(report.Blockers), "queue_consumer_stale") {
		t.Fatalf("queue poll did not expire: %+v %v", report, err)
	}
	inventory.Bindings[0].Access = "pull"
	report, err = Evaluate(inventory, policy, now.Add(time.Hour))
	if err != nil || !report.Passed || report.Coverage != "partial" || report.Bindings[0].Status != "unsupported" {
		t.Fatalf("external pull consumer claimed readiness or blocked: %+v %v", report, err)
	}
	inventory.Bindings[0].Access, inventory.Bindings[0].State = "push", "disabled"
	report, err = Evaluate(inventory, policy, now.Add(time.Hour))
	if err != nil || !report.Passed || report.Coverage != "none" || report.Bindings[0].Status != "skipped" {
		t.Fatalf("disabled queue affected readiness: %+v %v", report, err)
	}
}
