// adr: 431 — queue readiness cannot be waived with connectivity coverage.
package bindingcheck

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

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
