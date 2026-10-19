// adr: 431 — queue readiness cannot be waived with connectivity coverage.
package bindingcheck

import (
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (r *Report) checkQueueRequirements(inventory api.AppBindingInventory, policy Policy) {
	names := make([]string, 0, len(policy.QueueRequirements))
	for name := range policy.QueueRequirements {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		count := 0
		for _, item := range inventory.Bindings {
			if api.QueueBindingInScope(item, r.Scope) && item.Binding == name {
				count++
			}
		}
		code, message := "queue_binding_missing", "The required queue binding is absent from the selected deployment scope."
		if count > 1 {
			code, message = "queue_binding_ambiguous", "The required queue binding name is ambiguous between shared and environment bindings; select a unique binding name."
		}
		if count != 1 {
			r.Blockers = append(r.Blockers, Finding{Type: api.BindingTypeQueue, Binding: name, Scope: r.Scope, Code: code, Message: message})
		}
	}
}

func queueConsumerBlocker(item api.AppBindingInventoryItem, now time.Time) (string, string) {
	if item.Access == "pull" {
		return "", "" // External consumers have no scheduler readiness evidence.
	}
	if item.Access != "push" {
		return "queue_consumer_unknown", "Queue consumer mode is unknown; restore a push or pull binding and check again."
	}
	switch item.ConsumerState {
	case "not_configured":
		return "queue_consumer_missing", "The push consumer is missing; reconcile the queue binding before checking again."
	case "paused":
		return "queue_consumer_paused", "The push consumer is paused; enable its trigger and wait for a healthy scheduler poll."
	case "active":
	default:
		return "queue_consumer_unknown", "Push consumer state is unknown; restore consumer observations and check again."
	}
	if item.ObservedAt == nil || item.ObservedAt.IsZero() {
		return "queue_consumer_unobserved", "The push consumer has no poll timestamp; wait for a healthy scheduler poll."
	}
	if item.ObservedAt.After(now) {
		return "queue_consumer_time_future", "The push consumer poll timestamp is in the future; correct the scheduler clock and wait for a new poll."
	}
	if now.Sub(*item.ObservedAt) > api.QueueConsumerMaxPollAge || item.ConsumerLiveness == "stale" {
		return "queue_consumer_stale", "The push consumer poll is stale (maximum age 30 seconds); restore scheduler polling and check again."
	}
	switch item.ConsumerLiveness {
	case "healthy":
		return "", ""
	case "degraded":
		return "queue_consumer_degraded", "The push consumer's latest failure has not recovered; resolve the consumer error and wait for a successful poll."
	case "not_observed":
		return "queue_consumer_unobserved", "The push consumer has not been observed; wait for a healthy scheduler poll."
	default:
		return "queue_consumer_unknown", "Push consumer liveness is unknown; restore consumer observations and check again."
	}
}

func (r *Report) checkQueueCanary(item api.AppBindingInventoryItem, policy Policy, result *BindingResult) {
	if item.State != "enabled" || item.Access != "push" || item.QueueEnvironment == nil {
		r.bindingBlock(item, "queue_canary_unsupported", "Handler completion requires an enabled, scoped push consumer.")
		return
	}
	if *item.QueueEnvironment == "" && r.Scope != "production" && r.Scope != "default" {
		r.bindingBlock(item, "queue_canary_scope_unsupported", "Shared queue canaries support production/default only; use a queue binding for the selected environment.")
		return
	}
	if !item.QueueCanarySupported {
		code := item.QueueCanaryReason
		if code == "" {
			code = "queue_canary_unsupported"
		}
		r.bindingBlock(item, code, "The selected deployment and current consumer must be eligible for safe canary handling.")
		return
	}
	c := item.QueueCanary
	if c == nil || c.Status != "succeeded" || c.CompletedAt == nil || c.DeliveredAt == nil || c.DeploymentID != r.DeploymentID || c.Scope != r.Scope || c.Binding != item.Binding {
		r.bindingBlock(item, "queue_completion_unverified", "Publish and complete a canary through this binding on the exact selected deployment.")
		return
	}
	if c.CompletedAt.After(r.CheckedAt) || !c.ExpiresAt.After(r.CheckedAt) || r.CheckedAt.Sub(*c.CompletedAt) > policy.MaxVerificationAge {
		r.bindingBlock(item, "queue_completion_expired", "Queue completion evidence expired; verify again.")
		return
	}
	result.CheckedAt = c.CompletedAt
}
