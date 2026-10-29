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
