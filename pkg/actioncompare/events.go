// adr: 20261020090000
package actioncompare

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/behaviorcompare"
)

func eventGroupKey(parts ...string) string {
	body, _ := json.Marshal(parts)
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}

// addEventGroups compares the events an enrolled app published as a multiset
// per workload, source and type: schema version and canonical data must match,
// while the platform-generated event ID and time never do. Suppressed ordinary
// webhook events compare by multiplicity per workload and event type only,
// because their payloads carry platform-generated identifiers.
func addEventGroups(snapshot *api.ScenarioActionSnapshot, groups map[string]Change, ids map[string]bool) error {
	if snapshot.EventCaptureVersion == 0 {
		if len(snapshot.PublishedEvents) != 0 || len(snapshot.SuppressedWebhookEvents) != 0 || snapshot.IncompleteCapturedEvents != 0 {
			return errors.New("event evidence without an event capture version")
		}
		return nil
	}
	if snapshot.EventCaptureVersion != api.ScenarioEventCaptureVersion || snapshot.IncompleteCapturedEvents != 0 ||
		len(snapshot.PublishedEvents) > api.MaxScenarioCapturedEvents || len(snapshot.SuppressedWebhookEvents) > api.MaxScenarioCapturedEvents {
		return errors.New("event capture evidence is incomplete or exceeds format bounds")
	}
	workloads := map[string]bool{}
	for _, w := range snapshot.Workloads {
		workloads[w] = true
	}
	total := 0
	for _, event := range snapshot.PublishedEvents {
		id, err := uuid.Parse(event.ID)
		if err != nil || ids[id.String()] || !workloads[event.Workload] || event.Source == "" || len(event.Source) > 256 ||
			event.Type == "" || len(event.Type) > 256 || len(event.SchemaVersion) > 256 || len(event.Data) > api.MaxScenarioCapturedEventDataBytes {
			return errors.New("invalid captured event identity")
		}
		ids[id.String()] = true
		data, err := behaviorcompare.CanonicalJSON(event.Data)
		if err != nil {
			return errors.New("captured event data is ambiguous or invalid")
		}
		total += len(data)
		if total > api.MaxScenarioActionPayloadBytes {
			return errors.New("captured event data exceeds format bounds")
		}
		payload, _ := json.Marshal(struct {
			SchemaVersion string          `json:"schema_version,omitempty"`
			Data          json.RawMessage `json:"data"`
		}{event.SchemaVersion, data})
		key := eventGroupKey(event.Workload, "event", event.Source, event.Type)
		group := groups[key]
		if group.Key == "" {
			group = Change{Key: key, Workload: event.Workload, Source: "event", Name: event.Source}
		}
		group.Before = append(group.Before, Value{Type: event.Type, Payload: payload})
		groups[key] = group
	}
	seen := map[[2]string]bool{}
	for _, suppressed := range snapshot.SuppressedWebhookEvents {
		pair := [2]string{suppressed.Workload, suppressed.Event}
		if !workloads[suppressed.Workload] || suppressed.Event == "" || len(suppressed.Event) > 256 || seen[pair] ||
			suppressed.Count < 1 || suppressed.Count > api.MaxScenarioSuppressedEventCount {
			return errors.New("invalid suppressed webhook event inventory")
		}
		seen[pair] = true
		payload, _ := json.Marshal(struct {
			Count int64 `json:"count"`
		}{suppressed.Count})
		key := eventGroupKey(suppressed.Workload, "webhook_event", suppressed.Event)
		groups[key] = Change{Key: key, Workload: suppressed.Workload, Source: "webhook_event", Name: suppressed.Event,
			Before: []Value{{Type: suppressed.Event, Payload: payload}}}
	}
	return nil
}

// capturedEventCount counts published and suppressed events for summaries.
func capturedEventCount(snapshot *api.ScenarioActionSnapshot) int {
	count := len(snapshot.PublishedEvents)
	for _, suppressed := range snapshot.SuppressedWebhookEvents {
		count += int(suppressed.Count)
	}
	return count
}
