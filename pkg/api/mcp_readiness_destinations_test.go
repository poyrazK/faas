package api

import (
	"testing"
)

func TestMCPReadinessNotificationDestinationValidation(t *testing.T) {
	version := int64(0)
	request := MCPReadinessNotificationAction{RequestID: "00000000-0000-0000-0000-000000000001", EventID: "00000000-0000-0000-0000-000000000002", Action: "retry", ExpectedVersion: &version, Reason: "Receiver repaired"}
	for _, destination := range []string{"", "primary", "fallback", "unknown"} {
		request.Destination = destination
		if err := ValidateMCPReadinessNotificationAction(request); (err == nil) != (destination != "unknown") {
			t.Fatal(destination, err)
		}
	}
}
