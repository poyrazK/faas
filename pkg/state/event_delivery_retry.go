package state

import "encoding/json"

// IsEventDelivery reports whether an invocation delivers a published event
// to a subscription, first or as a replay: routing stamps the subscription
// id header on every delivery it admits. ADR-20261029190000: the drain paces
// such a delivery's retries by its app's retry policy or the event default.
func IsEventDelivery(inv Invocation) bool {
	if (inv.Source != InvocationAsyncInvoke && inv.Source != InvocationReplay) || len(inv.Headers) == 0 {
		return false
	}
	var headers map[string]string
	return json.Unmarshal(inv.Headers, &headers) == nil && headers["x-gregale-event-subscription-id"] != ""
}

// RetriesApplicationFailures reports whether a failure the app's own code
// returned (a thrown handler, a 408/429 or another 4xx) is settled by the
// retry-then-dead-letter rule of ADR-20261031100001 (amended 2026-11-07):
// every durable async source a caller cannot resend by itself. Cron runs
// keep their own failure rules and fire again on schedule; exclusive
// operations settle through their owners.
func RetriesApplicationFailures(inv Invocation) bool {
	switch inv.Source {
	case InvocationAsyncInvoke, InvocationReplay, InvocationInboundWebhook, InvocationQueue, InvocationDelayedTask:
		return true
	}
	return false
}
