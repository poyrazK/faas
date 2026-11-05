package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAutomationRetainedCountShowsUnknown(t *testing.T) {
	if got := automationRetainedEventCount(0, true); got != "unknown" {
		t.Fatalf("incomplete count: %q", got)
	}
	if got := automationRetainedEventCount(7, false); got != "7" {
		t.Fatalf("complete count: %q", got)
	}
	raw, err := json.Marshal(automationRecoveryPreview{RetainedEventsIncomplete: true})
	if err != nil || !strings.Contains(string(raw), `"retained_events_incomplete":true`) {
		t.Fatalf("incomplete recovery preview: %s %v", raw, err)
	}
}
