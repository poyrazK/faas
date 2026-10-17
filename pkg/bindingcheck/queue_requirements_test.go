package bindingcheck

import (
	"slices"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func readinessQueue(now time.Time) api.AppBindingInventoryItem {
	polled := now.Add(-time.Second)
	environment := "production"
	return api.AppBindingInventoryItem{Type: api.BindingTypeQueue, Name: "orders", Binding: "orders", Scope: environment, QueueEnvironment: &environment,
		State: "enabled", Access: "push", ConsumerState: "active", ConsumerLiveness: "healthy", ObservedAt: &polled}
}

func TestEvaluateQueueEnvironmentIsolation(t *testing.T) {
	for _, filter := range []string{"", "production"} {
		inventory, policy, now := fixture()
		inventory.Scope, policy.Scope = filter, filter
		queue := readinessQueue(now)
		stage, stale := "staging", now.Add(-time.Minute)
		queue.Scope, queue.QueueEnvironment, queue.ObservedAt, queue.ConsumerLiveness = stage, &stage, &stale, "stale"
		inventory.Bindings = append(inventory.Bindings, queue)
		report, err := Evaluate(inventory, policy, now)
		if err != nil || !report.Passed || report.Bindings[2].Status != "skipped" || report.Bindings[2].Reason != "outside_scope" {
			t.Fatalf("staging queue affected production: %+v %v", report, err)
		}
	}
}

func TestEvaluateQueueReadinessRequirement(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		mutate     func(*api.AppBindingInventoryItem, time.Time)
	}{
		{"healthy", "", func(_ *api.AppBindingInventoryItem, _ time.Time) {}},
		{"disabled", "binding_not_ready", func(b *api.AppBindingInventoryItem, _ time.Time) { b.State = "disabled" }},
		{"pull", "queue_readiness_unsupported", func(b *api.AppBindingInventoryItem, _ time.Time) { b.Access = "pull" }},
		{"old scope", "queue_scope_unconfirmed", func(b *api.AppBindingInventoryItem, _ time.Time) { b.QueueEnvironment = nil }},
		{"missing", "queue_consumer_missing", func(b *api.AppBindingInventoryItem, _ time.Time) { b.ConsumerState = "not_configured" }},
		{"paused", "queue_consumer_paused", func(b *api.AppBindingInventoryItem, _ time.Time) { b.ConsumerState = "paused" }},
		{"degraded", "queue_consumer_degraded", func(b *api.AppBindingInventoryItem, _ time.Time) { b.ConsumerLiveness = "degraded" }},
		{"unobserved", "queue_consumer_unobserved", func(b *api.AppBindingInventoryItem, _ time.Time) { b.ObservedAt = nil }},
		{"future", "queue_consumer_time_future", func(b *api.AppBindingInventoryItem, now time.Time) { at := now.Add(time.Second); b.ObservedAt = &at }},
		{"stale", "queue_consumer_stale", func(b *api.AppBindingInventoryItem, now time.Time) { at := now.Add(-time.Minute); b.ObservedAt = &at }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inventory, policy, now := fixture()
			queue := readinessQueue(now)
			tc.mutate(&queue, now)
			inventory.Bindings = append(inventory.Bindings, queue)
			policy.QueueRequirements = map[string]string{"orders": api.QueueEvidenceConsumerReadiness}
			report, err := Evaluate(inventory, policy, now)
			if err != nil || report.Passed != (tc.code == "") || report.Coverage != "partial" || report.AllowUnsupported || report.Bindings[2].RequiredQueueEvidence != api.QueueEvidenceConsumerReadiness || report.Bindings[2].CheckedAt != nil || !slices.Contains(codes(report.Warnings), "queue_connectivity_unverified") {
				t.Fatalf("readiness assessment: %+v %v", report, err)
			}
			if tc.code != "" && !slices.Contains(codes(report.Blockers), tc.code) {
				t.Fatalf("missing blocker %s: %+v", tc.code, report)
			}
		})
	}
}

func TestEvaluateQueueRequirementsCannotHideMissingAmbiguousOrOtherUnsupportedBindings(t *testing.T) {
	for _, scenario := range []string{"missing", "other scope", "ambiguous", "other queue", "outbound"} {
		t.Run(scenario, func(t *testing.T) {
			inventory, policy, now := fixture()
			queue := readinessQueue(now)
			policy.QueueRequirements = map[string]string{"orders": api.QueueEvidenceConsumerReadiness}
			want := "queue_binding_missing"
			switch scenario {
			case "missing":
			case "other scope":
				stage := "staging"
				queue.Scope, queue.QueueEnvironment = stage, &stage
				inventory.Bindings = append(inventory.Bindings, queue)
			case "ambiguous":
				shared := ""
				legacy := queue
				legacy.Scope, legacy.QueueEnvironment = "app", &shared
				inventory.Bindings = append(inventory.Bindings, queue, legacy)
				want = "queue_binding_ambiguous"
			case "other queue":
				other := queue
				other.Binding, other.Name = "other", "other"
				inventory.Bindings = append(inventory.Bindings, queue, other)
				want = "verification_unsupported"
			case "outbound":
				inventory.Bindings = append(inventory.Bindings, queue, api.AppBindingInventoryItem{Type: api.BindingTypeOutbound, Name: "provider", Scope: "app", State: "enabled"})
				want = "verification_unsupported"
			}
			report, err := Evaluate(inventory, policy, now)
			if err != nil || report.Passed || !slices.Contains(codes(report.Blockers), want) {
				t.Fatalf("requirement hid %s: %+v %v", scenario, report, err)
			}
		})
	}
}

func TestEvaluateQueueReadinessAgeIsIndependentOfProbeAge(t *testing.T) {
	inventory, policy, now := fixture()
	queue := readinessQueue(now)
	at := now.Add(-api.QueueConsumerMaxPollAge)
	queue.ObservedAt = &at
	inventory.Bindings = []api.AppBindingInventoryItem{queue}
	policy.QueueRequirements = map[string]string{"orders": api.QueueEvidenceConsumerReadiness}
	policy.MaxVerificationAge = time.Second
	report, err := Evaluate(inventory, policy, now)
	if err != nil || !report.Passed {
		t.Fatalf("queue age used probe age: %+v %v", report, err)
	}
	report, err = Evaluate(inventory, policy, now.Add(time.Nanosecond))
	if err != nil || report.Passed || !slices.Contains(codes(report.Blockers), "queue_consumer_stale") {
		t.Fatalf("queue poll did not expire: %+v %v", report, err)
	}
}

func TestEvaluateMigrationBindingsUseReleaseAudience(t *testing.T) {
	inventory, policy, now := fixture()
	inventory.Bindings = []api.AppBindingInventoryItem{{Type: api.BindingTypePostgres, Name: "primary", Binding: "MIGRATION_DATABASE_URL", Scope: "production", Access: "migration", State: "ready", VerificationStatus: "unknown"}}
	policy.RequireApplicationAck = true
	report, err := Evaluate(inventory, policy, now)
	if err != nil || !report.Passed || report.Coverage != "none" || report.Bindings[0].Status != "skipped" || report.Bindings[0].Reason != "release_only" || report.Bindings[0].ApplicationAdoption != nil || !slices.Contains(codes(report.Warnings), "binding_release_only") {
		t.Fatalf("release credentials required runtime evidence: %+v %v", report, err)
	}
}
