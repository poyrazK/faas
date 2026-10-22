// adr:1352
package bindingcheck

import (
	"github.com/onebox-faas/faas/pkg/api"
	"testing"
	"time"
)

func TestQueueCanaryPolicyCannotPassPollOrWrongCompletion(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*api.AppBindingInventoryItem)
	}{
		{"completed", func(_ *api.AppBindingInventoryItem) {}},
		{"poll only", func(i *api.AppBindingInventoryItem) { i.QueueCanary = nil }},
		{"wrong deployment", func(i *api.AppBindingInventoryItem) { i.QueueCanary.DeploymentID = "other" }},
		{"failed", func(i *api.AppBindingInventoryItem) { i.QueueCanary.Status = "failed" }},
		{"expired", func(i *api.AppBindingInventoryItem) { i.QueueCanary.ExpiresAt = time.Time{} }},
		{"shared stage", func(i *api.AppBindingInventoryItem) {
			environment := ""
			i.QueueEnvironment = &environment
			i.QueueCanary.Scope = "staging"
		}},
		{"undeclared", func(i *api.AppBindingInventoryItem) { i.QueueCanarySupported = false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inventory, policy, now := fixture()
			q := readinessQueue(now)
			q.QueueCanarySupported = true
			completed := now.Add(-time.Second)
			q.QueueCanary = &api.QueueBindingCanary{Binding: "orders", DeploymentID: inventory.VerificationDeploymentID, Scope: "production", Status: "succeeded", DeliveredAt: &completed, CompletedAt: &completed, ExpiresAt: now.Add(time.Minute)}
			tc.change(&q)
			if tc.name == "shared stage" {
				report := Report{Scope: "staging"}
				result := BindingResult{}
				report.checkQueueCanary(q, policy, &result)
				if len(report.Blockers) != 1 || report.Blockers[0].Code != "queue_canary_scope_unsupported" {
					t.Fatalf("shared queue crossed environments: %+v", report)
				}
				return
			}
			inventory.Bindings = append(inventory.Bindings, q)
			policy.QueueRequirements = map[string]string{"orders": api.QueueEvidenceHandlerCompletion}
			r, err := Evaluate(inventory, policy, now)
			if err != nil || r.Passed != (tc.name == "completed") {
				t.Fatalf("assessment: %+v %v", r, err)
			}
		})
	}
}

func TestQueueCanaryEligibilityBlocksReleasePlan(t *testing.T) {
	for _, reason := range []string{"queue_canary_context_unavailable", "queue_handler_unsupported"} {
		t.Run(reason, func(t *testing.T) {
			inventory, _, now := fixture()
			q := readinessQueue(now)
			q.QueueCanarySupported = false
			q.QueueCanaryReason = reason
			inventory.Bindings = []api.AppBindingInventoryItem{q}
			stored := api.BindingReleasePolicy{Mode: "enforce", Scope: "production", MaxVerificationAge: "5m", QueueRequirements: map[string]string{"orders": api.QueueEvidenceHandlerCompletion}}
			steps, blockers, _, err := VerificationPlan(inventory, stored, now)
			if err == nil || len(steps) != 1 || steps[0].Action != "blocked" || steps[0].Reason != reason || len(blockers) != 1 || blockers[0].Code != reason {
				t.Fatalf("unavailable canary became executable: %+v %+v %v", steps, blockers, err)
			}
		})
	}
}
