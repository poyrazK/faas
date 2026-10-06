package state_test

import (
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

// ADR-619: receipt reads and both scheduler routes must preserve the existing
// deterministic invocation identity across retries and code refactoring.
func TestPublishedEventInvocationIDCompatibility(t *testing.T) {
	const account = "00000000-0000-0000-0000-000000000001"
	got := state.PublishedEventInvocationID(account, "orders", "event-1", "subscription-1")
	if got != "fded0992-e24d-5f56-9138-6f0225f30011" {
		t.Fatalf("identity changed: %s", got)
	}
	for _, parts := range [][4]string{{account, "other-orders", "event-1", "subscription-1"}, {account, "orders", "event-2", "subscription-1"}, {account, "orders", "event-1", "subscription-2"}, {"00000000-0000-0000-0000-000000000002", "orders", "event-1", "subscription-1"}} {
		if state.PublishedEventInvocationID(parts[0], parts[1], parts[2], parts[3]) == got {
			t.Fatalf("identity collision: %+v", parts)
		}
	}
}
