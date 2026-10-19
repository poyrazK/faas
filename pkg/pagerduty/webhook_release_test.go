package pagerduty

import (
	"strings"
	"testing"
	"time"
)

func TestMCPReadinessPagerDutyUnacknowledgementParsing(t *testing.T) {
	now := time.Now().UTC()
	for _, region := range []string{"us", "eu"} {
		raw := strings.ReplaceAll(strings.ReplaceAll(string(acknowledgementBody(now, region)), "incident.acknowledged", "incident.unacknowledged"), `"status":"acknowledged"`, `"status":"triggered"`)
		if _, apply, err := ParseAcknowledgement([]byte(raw), now); err != nil || apply {
			t.Fatal("release enabled without opt-in", apply, err)
		}
		event, apply, err := ParseAcknowledgement([]byte(raw), now, true)
		if err != nil || !apply || event.Action != "release" || event.Region != region {
			t.Fatal(event, apply, err)
		}
		if _, apply, err := ParseAcknowledgement([]byte(strings.ReplaceAll(raw, `"status":"triggered"`, `"status":"acknowledged"`)), now, true); err == nil || apply {
			t.Fatal("invalid unacknowledgement accepted")
		}
		if _, apply, err := ParseAcknowledgement([]byte(strings.ReplaceAll(raw, "incident.unacknowledged", "incident.resolved")), now, true); err != nil || apply {
			t.Fatal("provider resolve changed health")
		}
		if _, apply, err := ParseAcknowledgement([]byte(strings.ReplaceAll(raw, `"event_type":"incident.unacknowledged"`, `"event_type":"incident.unacknowledged","EVENT_TYPE":"incident.acknowledged"`)), now, true); err == nil || apply {
			t.Fatal("ambiguous event accepted")
		}
	}
}
