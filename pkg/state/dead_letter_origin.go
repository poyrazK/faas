package state

import (
	"encoding/json"
	"strings"
)

// invocationDeadLetterOrigin distinguishes event-subscription deliveries
// from ordinary async invocations in the unified DLQ projection. The event id
// header is added by schedd at fan-out time and is safe to use as a durable
// source marker without changing the invocation source vocabulary.
func invocationDeadLetterOrigin(inv Invocation) string {
	var headers map[string]json.RawMessage
	if err := json.Unmarshal(inv.Headers, &headers); err == nil {
		var eventID string
		if err := json.Unmarshal(headers["x-gregale-event-id"], &eventID); err == nil && strings.TrimSpace(eventID) != "" {
			return "event_subscription"
		}
	}
	return string(inv.Source)
}
