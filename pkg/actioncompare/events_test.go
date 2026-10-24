// adr: 20261020090000
package actioncompare

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func eventEvidence(events ...api.ScenarioCapturedEvent) *Evidence {
	e := actionEvidence()
	e.Snapshot.EventCaptureVersion = api.ScenarioEventCaptureVersion
	e.Snapshot.PublishedEvents = events
	e.Snapshot.SuppressedWebhookEvents = []api.ScenarioSuppressedWebhookEvents{{Workload: "primary", Event: "deployment.live", Count: 1}}
	return e
}

func publishedEvent(data string) api.ScenarioCapturedEvent {
	return api.ScenarioCapturedEvent{ID: uuid.NewString(), Workload: "primary", Source: "orders", Type: "order.created", SchemaVersion: "1", Data: json.RawMessage(data)}
}

func TestCapturedEventsCompareBusinessDataAndMultiplicity(t *testing.T) {
	for _, tc := range []struct {
		name, status string
		mutate       func(*Evidence)
	}{
		{"independent-event-ids-and-equal-json", "passed", func(*Evidence) {}},
		{"duplicate-publish", "failed", func(e *Evidence) {
			e.Snapshot.PublishedEvents = append(e.Snapshot.PublishedEvents, publishedEvent(`{"order":1}`))
		}},
		{"missing-publish", "failed", func(e *Evidence) { e.Snapshot.PublishedEvents = nil }},
		{"data-changed", "failed", func(e *Evidence) { e.Snapshot.PublishedEvents[0].Data = json.RawMessage(`{"order":2}`) }},
		{"type-changed", "failed", func(e *Evidence) { e.Snapshot.PublishedEvents[0].Type = "order.cancelled" }},
		{"schema-version-changed", "failed", func(e *Evidence) { e.Snapshot.PublishedEvents[0].SchemaVersion = "2" }},
		{"extra-ordinary-webhook-event", "failed", func(e *Evidence) { e.Snapshot.SuppressedWebhookEvents[0].Count = 2 }},
		{"new-ordinary-webhook-event-type", "failed", func(e *Evidence) {
			e.Snapshot.SuppressedWebhookEvents = append(e.Snapshot.SuppressedWebhookEvents, api.ScenarioSuppressedWebhookEvents{Workload: "primary", Event: "instance.oom", Count: 1})
		}},
		{"older-server-without-event-capture", "inconclusive", func(e *Evidence) {
			e.Snapshot.EventCaptureVersion, e.Snapshot.PublishedEvents, e.Snapshot.SuppressedWebhookEvents = 0, nil, nil
		}},
		{"incomplete-event-capture", "inconclusive", func(e *Evidence) { e.Snapshot.IncompleteCapturedEvents = 1 }},
		{"event-evidence-without-version", "inconclusive", func(e *Evidence) { e.Snapshot.EventCaptureVersion = 0 }},
		{"event-for-unknown-workload", "inconclusive", func(e *Evidence) { e.Snapshot.PublishedEvents[0].Workload = "stranger" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := eventEvidence(publishedEvent(`{"order":1.0}`))
			after := eventEvidence(publishedEvent(`{"order":1}`))
			tc.mutate(after)
			result := Compare(before, after)
			if result.Status != tc.status {
				t.Fatalf("comparison=%+v, want %s", result, tc.status)
			}
			if result.Status == "failed" && len(result.Changes) == 0 {
				t.Fatal("event difference had no reviewable evidence")
			}
		})
	}
}
