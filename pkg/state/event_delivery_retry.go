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
// operations settle through their owners, and so does a sync invoke: its
// caller already holds the failure (amendment 2026-11-10).
func RetriesApplicationFailures(inv Invocation) bool {
	switch inv.Source {
	case InvocationAsyncInvoke:
		return !IsSyncInvoke(inv)
	case InvocationReplay, InvocationInboundWebhook, InvocationQueue, InvocationDelayedTask:
		return true
	}
	return false
}

// syncInvokeRetryKey marks the retry policy of a row that POST
// /v1/apps/{slug}/invoke enqueued. The sync endpoint reuses the async_invoke
// source and only its long-poll makes it synchronous, so the row carries the
// mark where every scheduler already reads per-row policy. An older scheduler
// ignores the key.
const syncInvokeRetryKey = "sync_invoke"

// MarkSyncInvokeRetryPolicy returns policy with the sync-invoke mark added.
// Hunt 32 H32-65: a sync invoke whose handler threw was retried for a day
// after its caller had received the error, waking (and billing) the app on
// every attempt with no way to cancel it.
func MarkSyncInvokeRetryPolicy(policy json.RawMessage) json.RawMessage {
	fields := map[string]json.RawMessage{}
	if len(policy) > 0 {
		if err := json.Unmarshal(policy, &fields); err != nil || fields == nil {
			fields = map[string]json.RawMessage{}
		}
	}
	fields[syncInvokeRetryKey] = json.RawMessage("true")
	marked, err := json.Marshal(fields)
	if err != nil {
		return policy
	}
	return marked
}

// IsSyncInvoke reports a row enqueued by the sync invoke endpoint. A replay
// of it is an ordinary async delivery and keeps the retry rule.
func IsSyncInvoke(inv Invocation) bool {
	if inv.Source != InvocationAsyncInvoke || len(inv.RetryPolicyJSON) == 0 {
		return false
	}
	var mark struct {
		SyncInvoke bool `json:"sync_invoke"`
	}
	return json.Unmarshal(inv.RetryPolicyJSON, &mark) == nil && mark.SyncInvoke
}
