// adr: 20261021120000
package state

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestScenarioEventCaptureDeduplicatesEventIdentity(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newEffectFixture(t, backend, actionCaptureTestRun)
			capturer := f.base.(ScenarioEventCaptureStore)
			event := ScenarioEventCapture{ID: "evt-1", AccountID: f.account.ID, Source: "orders", Type: "order.created", Data: json.RawMessage(`{"order":1}`)}
			if captured, err := capturer.CaptureScenarioEvent(t.Context(), f.app.ID, "", event); !captured || err != nil {
				t.Fatalf("first publish: %v %v", captured, err)
			}
			retry := event
			retry.Data = json.RawMessage(`{ "order" : 1.0 }`) // the same JSON value
			if captured, err := capturer.CaptureScenarioEvent(t.Context(), f.app.ID, "", retry); !captured || !errors.Is(err, ErrScenarioEventDuplicate) {
				t.Fatalf("retried publish: %v %v", captured, err)
			}
			changed := event
			changed.Data = json.RawMessage(`{"order":2}`)
			if captured, err := capturer.CaptureScenarioEvent(t.Context(), f.app.ID, "", changed); !captured || !errors.Is(err, ErrScenarioEventIdentityConflict) || !errors.Is(err, ErrConflict) {
				t.Fatalf("conflicting publish: %v %v", captured, err)
			}
			other := event
			other.Source = "billing" // identity is per source
			if captured, err := capturer.CaptureScenarioEvent(t.Context(), f.app.ID, "", other); !captured || err != nil {
				t.Fatalf("same id from another source: %v %v", captured, err)
			}
			snapshot := sealTestActions(t, f)
			if snapshot.Status != "complete" || len(snapshot.PublishedEvents) != 2 {
				t.Fatalf("sealed events = %+v (%s)", snapshot.PublishedEvents, snapshot.Status)
			}
		})
	}
}
